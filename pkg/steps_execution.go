// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// prTitle is the fixed PR title (Stage-1 contract).
const prTitle = "update go module dependencies"

// claudeExecutionTimeout bounds the execution Claude sub-call. Without it the
// sub-call is bounded only by the Job's activeDeadlineSeconds (1800s), which
// is not a bound the Go step survives: the deadline kills the whole pod, so
// the gate re-run, commit, and push after this call never happen and work that
// already passed its gates is discarded (observed 2026-08-16 on bborbe/ip —
// gate_exit: 0, branch never pushed, no PR).
//
// The point of the timeout is therefore not just failing fast; it is returning
// control to Go while there is still Job budget left to act on the result.
// Same rationale as bulkUpdateTimeout: exceeding it is a failure, never a
// retry.
const claudeExecutionTimeout = 15 * time.Minute

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
	runner         claudelib.ClaudeRunner
	ops            git.GitOps
	gh             GhCli
	gate           GateRunner
	bulk           BulkUpdater
	ghToken        string `display:"length"`
	prTarget       PRTarget
	autoMergeLabel string
	defaultScope   UpdateScope
}

// NewExecutionStep wires the execution step with its seams: the Claude
// runner (repair + CHANGELOG sub-call), the GitOps seam (clone/branch/commit/
// push), the gh CLI seam (PR create + adopt), the gate runner (deterministic
// green-gate re-run), the bulk updater (deterministic `go get -u ./...` +
// `go mod tidy`, run in Go so the model cannot background it), and the default
// update scope (UPDATE_SCOPE env; frontmatter `update_scope` overrides per
// task). prTarget selects draft or ready.
func NewExecutionStep(
	runner claudelib.ClaudeRunner,
	ops git.GitOps,
	gh GhCli,
	gate GateRunner,
	bulk BulkUpdater,
	ghToken string,
	prTarget PRTarget,
	autoMergeLabel string,
	defaultScope UpdateScope,
) agentlib.Step {
	return &executionStep{
		runner:         runner,
		ops:            ops,
		gh:             gh,
		gate:           gate,
		bulk:           bulk,
		ghToken:        ghToken,
		prTarget:       prTarget,
		autoMergeLabel: autoMergeLabel,
		defaultScope:   defaultScope,
	}
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
//  2. Read ## Plan (must show in-scope work per hasWorkForScope, not the
//     model's labels) + frontmatter.
//  3. PR-adopt guard — open PR for the deterministic branch → adopt, write
//     ## Result, re-route (crash-window idempotency, design § 5.3).
//  4. Resolve current default-branch HEAD at run start, CloneAtRef at it, +
//     SwitchNewBranch fix/update-go-<ref:7> (ref = pinned filing SHA —
//     replay guard).
//  5. Claude sub-call (file-edit + go/make only) — update + repair + CHANGELOG,
//     bounded by claudeExecutionTimeout. On failure the gates still decide:
//     green → salvage the work as a DRAFT PR, red → Failed with the Claude
//     cause (see salvageAfterClaudeFailure).
//  6. Deterministic gate re-run of the plan's gate targets → red = Failed
//     with the failing target + output tail.
//  7. No-effective-change guard — changed files empty or ⊆ {CHANGELOG.md}
//     (MVS no-op despite planning's has_work=true) → ## Result
//     outcome=no_update_needed, Done/NextPhase done, workdir discarded; no
//     commit/push/PR (go-skeleton PR #51 guard).
//  8. Changed/committed-files guard — reject .github/workflows/** edits.
//  9. Commit (explicit pathspec, bot identity) + Push (--no-follow-tags) +
//     gh pr create with the configured target.
//  10. ## Result → Done / NextPhase ai_review.
func (s *executionStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	if reroute := s.replayGuard(ctx, md); reroute != nil {
		return reroute, nil
	}

	repo, cloneURL, ref, err := s.extractFrontmatter(ctx, md)
	if err != nil {
		return s.fail(ctx, md, &ResultOutput{}, git.ErrorCategoryUnknown, err)
	}
	updateScope, err := resolveUpdateScope(ctx, md, s.defaultScope)
	if err != nil {
		return s.fail(ctx, md, &ResultOutput{}, git.ErrorCategoryUnknown,
			errors.Wrap(ctx, err, "invalid update_scope"))
	}
	glog.V(2).Infof("execution: update_scope=%s repo=%s", updateScope, repo)

	plan, err := s.validatePlan(ctx, md, updateScope)
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
	head, err := s.ops.ResolveDefaultBranchHead(ctx, authedURL)
	if err != nil {
		return s.fail(
			ctx,
			md,
			result,
			git.ClassifyError(err),
			errors.Wrap(ctx, err, "resolve current default-branch HEAD"),
		)
	}
	glog.V(2).Infof(
		"execution: pinned ref=%s (provenance only); clone base=resolved HEAD=%s",
		ref,
		head,
	)
	if err := s.ops.CloneAtRef(ctx, authedURL, head, workdir); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}
	if err := s.ops.SwitchNewBranch(ctx, workdir, branch); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}

	// Deterministic bulk update BEFORE the model call. Running it here is what
	// stops the model backgrounding a long `go get` and then blocking on
	// TaskOutput until the Job deadline kills it (see BulkUpdater).
	// golang-only scope skips the dep update — the model is told the bulk
	// update is out of scope (via the prompt section) instead of running it.
	bulkResult := BulkUpdateResult{Ran: false, FailDetail: "skipped: update_scope=golang"}
	if !updateScope.IsGolangOnly() {
		var bulkErr error
		bulkResult, bulkErr = s.bulk.Run(ctx, workdir)
		if bulkErr != nil {
			return s.fail(ctx, md, result, git.ErrorCategoryUnknown, bulkErr)
		}
	}

	report, claudeErr := s.runUpdate(ctx, workdir, plan, bulkResult, updateScope)
	if claudeErr != nil {
		return s.salvageAfterClaudeFailure(ctx, md, workdir, branch, plan, result, claudeErr)
	}

	if failResult, err := s.rerunGates(ctx, md, workdir, plan, result, report); failResult != nil ||
		err != nil {
		return failResult, err
	}

	return s.commitPushAndOpenPR(ctx, md, workdir, branch, plan, report, result, s.prTarget)
}

// salvageAfterClaudeFailure decides what to do when the Claude sub-call
// errored (timeout, permission refusal, CLI crash). It does NOT trust the
// model's own verdict, because the model does not have one — it failed. It
// asks the gates instead.
//
// Rationale: the gate targets are the deterministic verdict and the model's
// self-report never was. A run on 2026-08-16 reached gate_exit: 0 and was
// still discarded because the model died afterwards; the update was complete
// and green, and nothing was pushed. When the gates pass, the work on disk is
// good regardless of how the model exited.
//
// Two deliberate safety limits:
//   - The pull request is forced to DRAFT even on a deployment configured
//     PR_TARGET=ready. Green gates prove the code compiles and tests pass;
//     they do NOT prove the model finished its non-gated duties — most
//     importantly the CHANGELOG bullet, whose absence on an autoRelease repo
//     means the change merges but never ships. A human sees salvaged work.
//   - Gates red means no salvage: fail with the Claude error, which is the
//     more actionable cause.
func (s *executionStep) salvageAfterClaudeFailure(
	ctx context.Context,
	md *agentlib.Markdown,
	workdir, branch string,
	plan *PlanOutput,
	result *ResultOutput,
	claudeErr error,
) (*agentlib.Result, error) {
	// Classify rather than hardcoding unknown: a permission refusal and a
	// genuine model failure need different operator responses, and recording
	// both as "unknown" is what made the 2026-08-16 incident read as a
	// deadline problem instead of an allowlist one.
	category := git.ClassifyError(claudeErr)
	glog.Warningf(
		"execution: claude sub-call failed (category=%s) — re-running gates to "+
			"decide whether the work on disk is salvageable: %v",
		category, claudeErr,
	)

	// The model produced no parseable report, so PR body metadata (deps
	// counted, vulns fixed) is empty rather than guessed at.
	report := &executionReport{}
	if failResult, err := s.rerunGates(ctx, md, workdir, plan, result, report); err != nil {
		return failResult, err
	} else if failResult != nil {
		// rerunGates already wrote a ## Result with its own category; re-write
		// it so the recorded cause is the Claude failure that started this,
		// not the gate breakage that followed from it.
		return s.fail(ctx, md, result, category, claudeErr)
	}

	glog.Warningf(
		"execution: gates green despite claude failure — salvaging as a DRAFT pull "+
			"request (branch=%s); review the CHANGELOG before merging", branch,
	)
	return s.commitPushAndOpenPR(ctx, md, workdir, branch, plan, report, result, PRTargetDraft)
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

// validatePlan extracts and validates the ## Plan section. The readiness check
// mirrors planning's shouldClose: it runs off hasWorkForScope(scope), never off
// the model's outcome/HasWork labels. A label check here is the same bug as the
// planning `||` — on 2026-08-19 bborbe/log (update_scope=deps) carried
// outcome=no_update_needed + has_work=false while dep_updates_expected=true;
// v0.9.3 fixed planning to route it to execution, and this guard re-rejected
// it by trusting the model's boolean. The plan in markdown is already
// scope-filtered by planning's appliesScope, so hasWorkForScope on it is
// consistent with the decision that routed here.
func (s *executionStep) validatePlan(
	ctx context.Context,
	md *agentlib.Markdown,
	updateScope UpdateScope,
) (*PlanOutput, error) {
	plan, err := agentlib.ExtractSection[PlanOutput](ctx, md, "## Plan")
	if err != nil || plan == nil {
		return nil, errors.Wrapf(ctx, err, "execution invoked but planning did not complete")
	}
	if !plan.hasWorkForScope(updateScope) {
		return nil, errors.Errorf(
			ctx,
			"execution invoked but plan shows no in-scope work for scope=%s: "+
				"dep_updates_expected=%t vulns=%d go_bump=%t",
			updateScope, plan.DepUpdatesExpected, len(plan.Vulns), plan.GoBump != nil,
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

// bulkUpdateSection renders the deterministic bulk-update outcome for the
// prompt. Fail-closed: when the sequence did not run, the model is told so
// explicitly and instructed to run it itself in the foreground, rather than
// being left to assume the deps are current. The golang-only scope is the
// one deliberate exception: the bulk update is SKIPPED by design, not failed,
// and the model must not run it either.
func bulkUpdateSection(bulk BulkUpdateResult, scope UpdateScope) string {
	if scope.IsGolangOnly() {
		return "## Bulk update — SKIPPED\n\n" +
			"The update_scope is `golang`, so the deterministic bulk dependency " +
			"update (`go get -u ./...` + `go mod tidy`) was deliberately NOT run. " +
			"Do NOT run it and do not update module dependencies in this phase."
	}
	if bulk.Ran {
		return "## Bulk update — ALREADY DONE\n\n" +
			"`go get -u ./...` and `go mod tidy` have ALREADY been run for you in " +
			"this workdir. **Skip update-sequence step 3.** Do not re-run them and " +
			"do not background anything. Output:\n\n```\n" + bulk.Output + "\n```"
	}
	return "## Bulk update — DID NOT RUN\n\n" +
		"The deterministic bulk update FAILED, so the dependency graph is NOT " +
		"updated: " + bulk.FailDetail + "\n\nRun update-sequence step 3 yourself, " +
		"in the FOREGROUND. Output so far:\n\n```\n" + bulk.Output + "\n```"
}

// runUpdate issues the workdir-scoped Claude sub-call (targeted vuln fixes +
// repair-to-green + CHANGELOG bullet). The bulk dependency update already ran
// deterministically in Go before this call. The sub-call has NO git and NO gh
// tools — its tool scope is file-edit + go/make only.
func (s *executionStep) runUpdate(
	ctx context.Context,
	workdir string,
	plan *PlanOutput,
	bulk BulkUpdateResult,
	updateScope UpdateScope,
) (*executionReport, error) {
	planJSON, err := agentlib.MarshalSectionTyped(ctx, "## Plan", *plan)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal plan for prompt")
	}
	prompt := prompts.ExecutionPrompt() +
		"\n\n## Workdir\n\n" + workdir +
		"\n\n## Target Go\n\n" + targetGoVersion() +
		"\n\n" + updateScopeSection(updateScope) +
		"\n\n" + bulkUpdateSection(bulk, updateScope) +
		"\n\n" + planJSON.Heading + "\n\n" + planJSON.Body
	runCtx, cancel := context.WithTimeout(ctx, claudeExecutionTimeout)
	defer cancel()
	runResult, err := s.runner.Run(runCtx, prompt)
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
		// Each target is a `make` subprocess that can run for minutes. Without
		// this check a cancelled context still walks the whole target list,
		// which matters more now that the Job's remaining budget is what the
		// salvage path spends.
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx, ctx.Err(), "gate re-run cancelled")
		default:
		}
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

// commitPushAndOpenPR runs the no-effective-change guard, then the guarded
// commit → push → PR tail, and writes the successful ## Result.
//
// prTarget is passed in rather than read from s.prTarget because the salvage
// path forces draft regardless of deployment configuration — see
// salvageAfterClaudeFailure.
func (s *executionStep) commitPushAndOpenPR(
	ctx context.Context,
	md *agentlib.Markdown,
	workdir, branch string,
	plan *PlanOutput,
	report *executionReport,
	result *ResultOutput,
	prTarget PRTarget,
) (*agentlib.Result, error) {
	changed, err := s.ops.ChangedFiles(ctx, workdir)
	if err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
	}
	if isNoEffectiveChange(changed) {
		return s.noEffectiveChange(ctx, md, changed)
	}
	changed, err = s.filterWorkflowRegen(ctx, workdir, changed)
	if err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnexpectedDiff, err)
	}

	// Deterministic CHANGELOG guarantee (Defects 2/3/5). The model's bullet is
	// best-effort — and absent entirely on the salvage path — so Go guarantees
	// the CHANGELOG is bot-review-clean before the commit: canonical preamble
	// on fresh files, `## Unreleased` after the preamble, and a `chore:` bullet
	// naming the actual bumps. The base go.mod for the diff comes from
	// origin/master; when it cannot be read (e.g. repo has no go.mod), the
	// writer falls back to the generic bullet.
	if _, err := os.Stat(workdir); err == nil {
		baseGoMod, _ := s.ops.ShowFile(ctx, workdir, "origin/master", "go.mod")
		if _, err := EnsureChangelog(ctx, workdir, baseGoMod); err != nil {
			return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
		}
		// The writer may have created/modified CHANGELOG.md — re-fetch so the
		// commit pathspec and the post-commit guard see it.
		changed, err = s.ops.ChangedFiles(ctx, workdir)
		if err != nil {
			return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
		}
	}

	if _, err := s.ops.Commit(ctx, workdir, prTitle, changed...); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}

	// Post-commit guard (belt + suspenders): the release trust model depends
	// on the commit containing only the guarded change set. Mirrors the
	// pre-commit classification: legitimately-regenerated workflow files (the
	// update's own doing, committed above) are allowed; a no-op regeneration
	// or an unrelated edit in the commit is still refused.
	committed, err := s.ops.CommittedFiles(ctx, workdir)
	if err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)
	}
	if err := s.verifyCommittedWorkflows(ctx, workdir, committed); err != nil {
		return s.fail(ctx, md, result, git.ErrorCategoryUnexpectedDiff, err)
	}

	if err := s.ops.Push(ctx, workdir, branch); err != nil {
		return s.fail(ctx, md, result, git.ClassifyError(err), err)
	}

	vulnsFixed := intersectFixVulns(plan, report)
	prURL, err := s.gh.CreatePR(
		ctx, workdir, "master", branch, prTitle,
		buildPRBody(plan, report, vulnsFixed),
		prTarget,
		s.autoMergeLabel,
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

	glog.V(2).Infof("execution: PR opened (target=%s) %s branch=%s", prTarget, prURL, branch)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseAIReview.String(),
	}, nil
}

// noEffectiveChange writes ## Result(outcome=no_update_needed) and routes to
// done — the same terminal-success shape planning's own no-op path uses
// (design guard for the go-skeleton PR #51 incident: planning's go-list scan
// can classify has_work=true on stale INDIRECT deps that go get -u ./... +
// go mod tidy no-op under MVS resolution; the only diff left in that run was
// the CHANGELOG bullet Claude wrote describing dependency updates that don't
// exist). Nothing is committed, pushed, or opened as a PR — the workdir
// (and any CHANGELOG-only edit in it) is simply discarded by the caller's
// deferred cleanup.
func (s *executionStep) noEffectiveChange(
	ctx context.Context,
	md *agentlib.Markdown,
	changed []string,
) (*agentlib.Result, error) {
	glog.V(2).Infof(
		"execution: no effective change after claude sub-call (changed=%v) — discarding workdir",
		changed,
	)
	output := ResultOutput{Outcome: ResultOutcomeNoUpdateNeeded}
	section, err := agentlib.MarshalSectionTyped(ctx, "## Result", output)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Result section (no_update_needed)")
	}
	md.ReplaceSection(section)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseDone.String(),
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

// isNoEffectiveChange reports whether changed is empty or a subset of
// {CHANGELOG.md} — i.e. go get -u ./... + go mod tidy produced nothing under
// MVS resolution and the only diff left is the CHANGELOG bullet the Claude
// sub-call writes to describe the (nonexistent) update. This is the
// go-skeleton PR #51 guard: a plan that classified has_work=true off
// go-list's INDIRECT-deps view must not turn into a draft PR containing only
// a fabricated CHANGELOG entry.
func isNoEffectiveChange(changed []string) bool {
	for _, p := range changed {
		if p != changelogFileName {
			return false
		}
	}
	return true
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

// classifyWorkflowChanges splits changed `.github/workflows/*` paths into
// legitimate regenerations (the update's own doing — include in the commit)
// and no-op regenerations (byte-identical to base — skip). The model is
// architecturally forbidden from editing workflows (no git/gh tools in its
// tool scope, the execution prompt forbids `.github/workflows/`, and the
// GitHub App lacks the Workflows permission — design D3), so a workflow-file
// content change present at commit time can only be a deterministic
// regeneration side-effect of the update's own tooling (e.g. a maintainer
// dep-bump rewriting `.github/workflows/*`, `.golangci.yml`,
// `.maintainer.yaml`).
//
// A path whose base (origin/master) version cannot be read is a brand-new
// workflow file the update could not have regenerated — an unrelated edit,
// reported as an error so the caller keeps the forbidden-path guard.
func (s *executionStep) classifyWorkflowChanges(
	ctx context.Context,
	workdir string,
	offending []string,
) (legit, noop []string, err error) {
	for _, p := range offending {
		// Each iteration shells out (ShowFile runs a git subprocess) — check
		// ctx.Done() between paths, same shape as rerunGates.
		select {
		case <-ctx.Done():
			return nil, nil, errors.Wrap(ctx, ctx.Err(), "workflow regeneration check cancelled")
		default:
		}
		// #nosec G304 -- p comes from the repo's own git-status changed-files
		// list (agent-controlled, same trust boundary as the `git add -- <paths>`
		// pathspec annotated G204 in os_exec_git_ops.go); workdir is os.TempDir-
		// rooted.
		work, rerr := os.ReadFile(filepath.Join(workdir, p))
		if rerr != nil {
			return nil, nil, errors.Wrap(ctx, rerr, "read working-tree workflow file "+p)
		}
		base, berr := s.ops.ShowFile(ctx, workdir, "origin/master", p)
		if berr != nil {
			return nil, nil, errors.Wrap(ctx, berr, "read base workflow file "+p)
		}
		if bytes.Equal(work, base) {
			noop = append(noop, p)
		} else {
			legit = append(legit, p)
		}
	}
	return legit, noop, nil
}

// filterWorkflowRegen applies the forbidden-workflow-path guard to the changed
// set and returns the filtered list to commit. The guard is belt+suspenders
// against the model silently rewriting CI (design D3: App lacks Workflows
// permission; the execution prompt forbids `.github/workflows/`). But a
// maintainer dep-bump can legitimately REGENERATE `.github/workflows/*` (and
// `.golangci.yml` / `.maintainer.yaml`) in the working tree as a deterministic
// side-effect of the update's own tooling. Distinguish the two:
//   - legit regeneration (content differs from base) → include in commit
//   - no-op regeneration (byte-identical to base)     → skip
//   - brand-new workflow file (no base version)        → still fail
func (s *executionStep) filterWorkflowRegen(
	ctx context.Context,
	workdir string,
	changed []string,
) ([]string, error) {
	offending := workflowPaths(changed)
	if len(offending) == 0 {
		return changed, nil
	}
	legit, noop, err := s.classifyWorkflowChanges(ctx, workdir, offending)
	if err != nil {
		return nil, errors.Errorf(ctx,
			"update touched forbidden workflow paths %v — refusing to commit", offending)
	}
	if len(noop) > 0 {
		glog.V(2).Infof(
			"execution: workflow regeneration no-op, skipping paths=%v", noop)
		changed = dropPaths(changed, noop)
	}
	if len(legit) > 0 {
		glog.V(2).Infof(
			"execution: workflow regeneration legit, committing paths=%v", legit)
	}
	return changed, nil
}

// verifyCommittedWorkflows is the post-commit belt+suspenders guard: the
// committed set must contain no workflow paths other than legitimately-
// regenerated ones (committed by filterWorkflowRegen above). A no-op
// regeneration or an unrelated edit in the commit is refused before push.
func (s *executionStep) verifyCommittedWorkflows(
	ctx context.Context,
	workdir string,
	committed []string,
) error {
	offending := workflowPaths(committed)
	if len(offending) == 0 {
		return nil
	}
	if _, noop, err := s.classifyWorkflowChanges(ctx, workdir, offending); err != nil ||
		len(noop) > 0 {
		return errors.Errorf(ctx,
			"commit contains forbidden workflow paths %v — refusing to push", offending)
	}
	return nil
}

// dropPaths returns paths with every entry in drop removed, preserving order.
func dropPaths(paths, drop []string) []string {
	dropSet := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropSet[d] = struct{}{}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, ok := dropSet[p]; !ok {
			out = append(out, p)
		}
	}
	return out
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
