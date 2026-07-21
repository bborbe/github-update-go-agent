// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"os"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-update-go-agent/pkg/git"
	"github.com/bborbe/github-update-go-agent/pkg/prompts"
)

// branchPrefix is the deterministic work-branch prefix; the full branch is
// branchPrefix + ref[:7]. Determinism is the crash-window replay guard: a
// replayed task computes the same branch and adopts the existing PR instead
// of pushing a duplicate (design § 5.3).
const branchPrefix = "fix/update-go-"

// prTitle is the fixed draft-PR title (Stage-1 contract).
const prTitle = "update go module dependencies"

// workflowsPrefix is the forbidden commit path. The committed-files guard
// rejects it before push AND the GitHub App lacks the Workflows permission
// (design D3) — belt + suspenders.
const workflowsPrefix = ".github/workflows/"

// executionReport is the small JSON the execution Claude sub-call returns:
// metadata about what it changed. Best-effort — the deterministic gate
// re-run is the actual verdict.
type executionReport struct {
	DepsUpdated int      `json:"deps_updated"`
	VulnsFixed  []string `json:"vulns_fixed"`
	Notes       string   `json:"notes"`
	Blocked     string   `json:"blocked"`
}

// executionStep implements agentlib.Step for the execution phase: a custom
// Go step embedding one Claude sub-call. All git/gh side effects are the Go
// step's — the Claude sub-call has NO git and NO gh tools (design § 7.0).
type executionStep struct {
	runner  claudelib.ClaudeRunner
	ops     git.GitOps
	gh      GhCli
	gate    GateRunner
	ghToken string
}

// NewExecutionStep wires the execution step with its four seams: the Claude
// runner (update + repair sub-call), the GitOps seam (clone/branch/commit/
// push), the gh CLI seam (draft PR create + adopt), and the gate runner
// (deterministic green-gate re-run).
func NewExecutionStep(
	runner claudelib.ClaudeRunner,
	ops git.GitOps,
	gh GhCli,
	gate GateRunner,
	ghToken string,
) agentlib.Step {
	return &executionStep{runner: runner, ops: ops, gh: gh, gate: gate, ghToken: ghToken}
}

// Name implements agentlib.Step.
func (s *executionStep) Name() string { return "github-update-go-execute" }

// ShouldRun always returns true; the replay guard lives inside Run
// (dark-factory pattern) so a crash-window replay can re-route without
// redoing side effects.
func (s *executionStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run executes the update pipeline:
//  1. Replay guard — existing successful ## Result → re-route to ai_review.
//  2. Read ## Plan (must be ready + has_work) + frontmatter.
//  3. PR-adopt guard — open PR for the deterministic branch → adopt, write
//     ## Result, re-route (crash-window idempotency, design § 5.3).
//  4. CloneAtRef + SwitchNewBranch fix/update-go-<ref:7>.
//  5. Claude sub-call (file-edit + go/make only) — update + repair + CHANGELOG.
//  6. Deterministic gate re-run of the plan's gate targets → red = Failed
//     with the failing target + output tail.
//  7. Changed/committed-files guard — reject .github/workflows/** edits.
//  8. Commit (explicit pathspec, bot identity) + Push (--no-follow-tags) +
//     gh pr create --draft.
//  9. ## Result → Done / NextPhase ai_review.
func (s *executionStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	if reroute := s.replayGuard(ctx, md); reroute != nil {
		return reroute, nil
	}

	plan, err := s.validatePlan(ctx, md)
	if err != nil {
		return s.fail(ctx, md, &ResultOutput{}, git.ErrorCategoryUnknown, err)
	}

	repo, cloneURL, ref, err := s.extractFrontmatter(ctx, md)
	if err != nil {
		return s.fail(ctx, md, &ResultOutput{}, git.ErrorCategoryUnknown, err)
	}
	branch := branchPrefix + ref[:7]

	if adopt := s.adoptExistingPR(ctx, md, repo, branch); adopt != nil {
		return adopt, nil
	}

	workdir := setupWorkdir(md, repo)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf("execution: workdir cleanup failed: path=%s err=%v", workdir, err)
		}
	}()

	result := &ResultOutput{Branch: branch}
	authedURL := injectToken(normalizeCloneURLToHTTPS(cloneURL), s.ghToken)
	if err := s.ops.CloneAtRef(ctx, authedURL, ref, workdir); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}
	if err := s.ops.SwitchNewBranch(ctx, workdir, branch); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}

	report, claudeErr := s.runUpdate(ctx, workdir, plan)
	if claudeErr != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown, claudeErr)
	}

	if failResult, err := s.rerunGates(ctx, md, workdir, plan, result, report); failResult != nil ||
		err != nil {
		return failResult, err
	}

	return s.commitPushAndOpenPR(ctx, md, workdir, branch, plan, report, result)
}

// replayGuard re-routes when a prior run already produced a successful
// ## Result — the draft PR exists; redoing clone/push would duplicate work.
// A failed ## Result does NOT re-route: the phase is the resume cursor and
// a retry re-runs the pipeline.
func (s *executionStep) replayGuard(
	ctx context.Context,
	md *agentlib.Markdown,
) *agentlib.Result {
	prior, err := agentlib.ExtractSection[ResultOutput](ctx, md, "## Result")
	if err != nil || prior == nil {
		return nil
	}
	if prior.Outcome != ResultOutcomeOpened && prior.Outcome != ResultOutcomeAdopted {
		return nil
	}
	glog.V(2).Infof(
		"execution: replay guard — ## Result already %s (pr=%s), re-routing to ai_review",
		prior.Outcome, prior.PRURL,
	)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseAIReview.String(),
	}
}

// adoptExistingPR is the crash-window guard: the branch name is
// deterministic, so if an open PR for it already exists (a prior run pushed
// + created the PR but crashed before ## Result landed), adopt it instead of
// re-pushing. Errors are logged and ignored — the subsequent push would
// surface a real problem loudly.
func (s *executionStep) adoptExistingPR(
	ctx context.Context,
	md *agentlib.Markdown,
	repo, branch string,
) *agentlib.Result {
	url, err := s.gh.FindOpenPRByHead(ctx, repo, branch)
	if err != nil {
		glog.Warningf("execution: pr-adopt lookup failed (continuing): %v", err)
		return nil
	}
	if url == "" {
		return nil
	}
	glog.V(2).Infof("execution: adopting existing PR %s for branch %s", url, branch)
	output := ResultOutput{
		Outcome: ResultOutcomeAdopted,
		Branch:  branch,
		PRURL:   url,
	}
	section, err := agentlib.MarshalSectionTyped(ctx, "## Result", output)
	if err != nil {
		glog.Warningf("execution: marshal adopted ## Result failed: %v", err)
		return nil
	}
	md.ReplaceSection(section)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseAIReview.String(),
	}
}

// validatePlan extracts and validates the ## Plan section.
func (s *executionStep) validatePlan(
	ctx context.Context,
	md *agentlib.Markdown,
) (*PlanOutput, error) {
	plan, err := agentlib.ExtractSection[PlanOutput](ctx, md, "## Plan")
	if err != nil || plan == nil {
		return nil, errors.Wrapf(ctx, err, "execution invoked but planning did not complete")
	}
	if plan.Outcome != PlanOutcomeReady || !plan.HasWork {
		return nil, errors.Errorf(
			ctx,
			"execution invoked with non-ready plan: outcome=%s has_work=%t",
			plan.Outcome, plan.HasWork,
		)
	}
	if len(plan.GateTargets) == 0 {
		return nil, errors.Errorf(ctx, "execution invoked with empty gate_targets")
	}
	return plan, nil
}

// extractFrontmatter reads the required frontmatter fields.
func (s *executionStep) extractFrontmatter(
	ctx context.Context,
	md *agentlib.Markdown,
) (repo, cloneURL, ref string, _ error) {
	repo, _ = md.Frontmatter.String("repo")
	cloneURL, _ = md.Frontmatter.String("clone_url")
	ref, _ = md.Frontmatter.String("ref")
	if repo == "" || cloneURL == "" || ref == "" {
		return "", "", "", errors.Errorf(
			ctx,
			"missing frontmatter: repo=%q clone_url=%q ref=%q",
			repo, cloneURL, ref,
		)
	}
	if len(ref) < 7 {
		return "", "", "", errors.Errorf(ctx, "frontmatter ref too short for branch name: %q", ref)
	}
	return repo, cloneURL, ref, nil
}

// runUpdate issues the workdir-scoped Claude sub-call (update sequence +
// repair-to-green + CHANGELOG bullet). The sub-call has NO git and NO gh
// tools — its tool scope is file-edit + go/make only.
func (s *executionStep) runUpdate(
	ctx context.Context,
	workdir string,
	plan *PlanOutput,
) (*executionReport, error) {
	planJSON, err := agentlib.MarshalSectionTyped(ctx, "## Plan", *plan)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal plan for prompt")
	}
	prompt := prompts.ExecutionPrompt() +
		"\n\n## Workdir\n\n" + workdir +
		"\n\n## Target Go\n\n" + targetGoVersion() +
		"\n\n" + planJSON.Heading + "\n\n" + planJSON.Body
	runResult, err := s.runner.Run(ctx, prompt)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "claude execution run")
	}
	report, perr := parseJSONResponse[executionReport](ctx, runResult.Result)
	if perr != nil {
		// Best-effort metadata — the deterministic gate re-run below is the
		// actual verdict; do not fail the pipeline over report formatting.
		glog.Warningf("execution: parse claude report failed (continuing): %v", perr)
		return &executionReport{}, nil
	}
	return report, nil
}

// rerunGates re-runs every planned gate target deterministically. Returns a
// non-nil failResult when a target stays red (design: Status Failed with the
// failing target + output tail in Message; resume cursor = execution).
func (s *executionStep) rerunGates(
	ctx context.Context,
	md *agentlib.Markdown,
	workdir string,
	plan *PlanOutput,
	result *ResultOutput,
	report *executionReport,
) (*agentlib.Result, error) {
	for _, target := range plan.GateTargets {
		tail, exitCode, err := s.gate.RunTarget(ctx, workdir, target)
		if err != nil {
			result.GateExit = exitCode
			result.FailedTarget = target
			msg := fmt.Sprintf("gate target %q failed (exit %d): %s", target, exitCode, tail)
			if report.Blocked != "" {
				msg += " — claude reported blocked: " + report.Blocked
			}
			return s.fail(ctx, md, result, git.ErrorCategoryUnknown, errors.Errorf(ctx, "%s", msg))
		}
	}
	result.GateExit = 0
	return nil, nil
}

// commitPushAndOpenPR runs the guarded commit → push → draft-PR tail and
// writes the successful ## Result.
func (s *executionStep) commitPushAndOpenPR(
	ctx context.Context,
	md *agentlib.Markdown,
	workdir, branch string,
	plan *PlanOutput,
	report *executionReport,
	result *ResultOutput,
) (*agentlib.Result, error) {
	changed, err := s.ops.ChangedFiles(ctx, workdir)
	if err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
	}
	if len(changed) == 0 {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown,
			errors.Errorf(ctx, "claude sub-call produced no file changes"))
	}
	if offending := workflowPaths(changed); len(offending) > 0 {
		return s.fail(ctx, md, result, git.ErrorCategoryUnexpectedDiff,
			errors.Errorf(ctx,
				"update touched forbidden workflow paths %v — refusing to commit", offending))
	}

	if _, err := s.ops.Commit(ctx, workdir, prTitle, changed...); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}

	// Post-commit guard (belt + suspenders): the release trust model depends
	// on the commit containing only the guarded change set.
	committed, err := s.ops.CommittedFiles(ctx, workdir)
	if err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
	}
	if offending := workflowPaths(committed); len(offending) > 0 {
		return s.fail(ctx, md, result, git.ErrorCategoryUnexpectedDiff,
			errors.Errorf(ctx,
				"commit contains forbidden workflow paths %v — refusing to push", offending))
	}

	if err := s.ops.Push(ctx, workdir, branch); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}

	vulnsFixed := intersectFixVulns(plan, report)
	prURL, err := s.gh.CreateDraftPR(
		ctx, workdir, "master", branch, prTitle,
		buildPRBody(plan, report, vulnsFixed),
	)
	if err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
	}

	result.Outcome = ResultOutcomeOpened
	result.PRURL = prURL
	result.DepsUpdated = report.DepsUpdated
	result.VulnsFixed = vulnsFixed
	section, err := agentlib.MarshalSectionTyped(ctx, "## Result", *result)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Result section")
	}
	md.ReplaceSection(section)

	glog.V(2).Infof("execution: draft PR opened %s branch=%s", prURL, branch)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseAIReview.String(),
	}, nil
}

// fail writes a ## Result(outcome=failed) section with the supplied
// error_category + redacted error string, and returns Status=Failed for
// controller retry (resume cursor = execution; the step never writes
// ## Failure, never mutates assignee/status, never routes human_review).
func (s *executionStep) fail(
	ctx context.Context,
	md *agentlib.Markdown,
	result *ResultOutput,
	category git.ErrorCategory,
	cause error,
) (*agentlib.Result, error) {
	msg := ""
	if cause != nil {
		msg = git.RedactToken(cause.Error())
	}
	output := *result
	output.Outcome = ResultOutcomeFailed
	output.ErrorCategory = category
	output.Error = msg
	section, err := agentlib.MarshalSectionTyped(ctx, "## Result", output)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Result section (failed)")
	}
	md.ReplaceSection(section)
	glog.V(2).Infof("execution failed: category=%s err=%s", category, msg)
	return &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: msg,
	}, nil
}

// workflowPaths returns the paths under .github/workflows/ (forbidden set).
func workflowPaths(paths []string) []string {
	var offending []string
	for _, p := range paths {
		if strings.HasPrefix(p, workflowsPrefix) {
			offending = append(offending, p)
		}
	}
	return offending
}

// intersectFixVulns enforces the design § 4.4 invariant
// Result.vulns_fixed ⊆ {v.id | v ∈ Plan.vulns, action=fix}. When the Claude
// report carried IDs, they are filtered against the plan's fix set; when the
// report was empty/unparseable, the plan's full fix set is used — justified
// because the deterministic green gate proves the scanners are clean.
func intersectFixVulns(plan *PlanOutput, report *executionReport) []string {
	fixSet := map[string]struct{}{}
	var planFix []string
	for _, v := range plan.Vulns {
		if v.Action == VulnActionFix {
			fixSet[v.ID] = struct{}{}
			planFix = append(planFix, v.ID)
		}
	}
	if len(report.VulnsFixed) == 0 {
		return planFix
	}
	var out []string
	for _, id := range report.VulnsFixed {
		if _, ok := fixSet[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// buildPRBody assembles the informative draft-PR body: what changed, the
// green gate evidence, and the release-agent handoff note. No secrets, no
// attribution.
func buildPRBody(plan *PlanOutput, report *executionReport, vulnsFixed []string) string {
	var b strings.Builder
	b.WriteString("Automated Go toolchain + dependency update.\n\n")
	if plan.GoBump != nil {
		fmt.Fprintf(&b, "- go directive: %s -> %s\n", plan.GoBump.From, plan.GoBump.To)
	}
	if report.DepsUpdated > 0 {
		fmt.Fprintf(&b, "- dependencies updated: %d\n", report.DepsUpdated)
	}
	if len(vulnsFixed) > 0 {
		fmt.Fprintf(&b, "- vulnerabilities fixed: %s\n", strings.Join(vulnsFixed, ", "))
	}
	fmt.Fprintf(&b, "- gate green: %s (exit 0)\n", strings.Join(plan.GateTargets, ", "))
	if report.Notes != "" {
		fmt.Fprintf(&b, "- notes: %s\n", report.Notes)
	}
	b.WriteString(
		"\nCHANGELOG entry stays under `## Unreleased` — the release agent versions and tags on merge.\n",
	)
	return b.String()
}
