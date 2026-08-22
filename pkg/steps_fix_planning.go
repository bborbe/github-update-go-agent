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

// fixWorkdirPrefix roots every ephemeral build-fix clone under os.TempDir().
const fixWorkdirPrefix = "github-build-fix-"

// fixRequiredFrontmatterFields are the keys read from the task's frontmatter
// before the planning step does any IO. Missing OR empty → needs_input naming
// the field (message only — the controller owns the escalation envelope).
//
// Order matters for deterministic error messages: first missing field wins.
var fixRequiredFrontmatterFields = []string{
	"repo",
	"episode_sha",
}

// fixPlanningStep implements agentlib.Step for the build-fix planning phase:
// verify green at HEAD, fetch failed logs, run the Claude diagnosis call
// against them, classify into one of the four fix verdicts (no_fix_needed /
// chain_update / file_spec / needs_input), and write ## Fix Plan.
type fixPlanningStep struct {
	runner  claudelib.ClaudeRunner
	ops     git.GitOps
	gh      GhCli
	ghToken string `display:"length"`
}

// NewFixPlanningStep wires the build-fix planning step with its Claude
// runner (diagnosis LLM), the GitOps seam (clone at episode SHA), the gh
// CLI seam (run log fetch + green-at-HEAD check), and the GitHub token for
// HTTPS auth URL transformation.
func NewFixPlanningStep(
	runner claudelib.ClaudeRunner,
	ops git.GitOps,
	gh GhCli,
	ghToken string,
) agentlib.Step {
	return &fixPlanningStep{
		runner:  runner,
		ops:     ops,
		gh:      gh,
		ghToken: ghToken,
	}
}

// Name implements agentlib.Step.
func (s *fixPlanningStep) Name() string { return "build-fix-plan" }

// ShouldRun always returns true — planning is idempotent: a re-trigger
// re-clones, re-fetches logs, and replaces the existing ## Fix Plan section
// in place (the operator re-delegate loop depends on a fresh diagnosis).
func (s *fixPlanningStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run executes the build-fix planning pipeline:
//  1. Required-frontmatter validation → NeedsInput (message only; the step
//     NEVER writes ## Failure and NEVER mutates assignee/status — the
//     controller owns the escalation envelope).
//  2. Clone at episode SHA via GitOps → Failed on clone/auth error.
//  3. Verify build state at HEAD: if green → ## Fix Plan + Done/NextPhase
//     done (no_fix_needed, task completes). Fetch failed workflow logs via
//     the gh CLI seam.
//  4. Claude diagnosis call against the failing-workflow + log evidence →
//     parse FixPlanOutput; validate the verdict is one of the four known
//     values (a fabricated verdict → Failed naming it).
//  5. needs_input → ## Fix Plan + NeedsInput (escalate, message only).
//  6. Otherwise → ## Fix Plan + Done/NextPhase execution (chain or file spec).
func (s *fixPlanningStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	missingField, repo, episodeSHA, err := readFixRequired(ctx, md)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "read required frontmatter")
	}
	if missingField != "" {
		glog.V(2).
			Infof("build-fix planning: missing frontmatter field=%s — escalating", missingField)
		return needsInput("required frontmatter field missing: " + missingField), nil
	}

	workdir := s.setupFixWorkdir(md)

	// Clone at the episode SHA so the diagnosis sees the exact tree that
	// failed. If the SHA is not fetchable (force-push), fall back to default
	// branch — the diagnosis records the gap in the reason.
	cloneURL := injectToken(normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"), s.ghToken)
	if err := s.ops.CloneAtRef(ctx, cloneURL, episodeSHA, workdir); err != nil {
		glog.V(2).
			Infof("build-fix planning: clone@episode failed (episode_sha=%s repo=%s) — retrying at default branch", episodeSHA, repo)
		if err := s.ops.CloneAtRef(ctx, cloneURL, "HEAD", workdir); err != nil {
			return s.failFixClone(repo, err), nil
		}
	}

	plan, failResult := s.runDiagnosis(ctx, md, repo, episodeSHA)
	if failResult != nil {
		return failResult, nil
	}
	if err := writeFixPlanSection(ctx, md, plan); err != nil {
		return nil, err
	}

	switch plan.Verdict {
	case FixVerdictNoFixNeeded:
		glog.V(2).Infof("build-fix planning: build already green repo=%s", repo)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: domain.TaskPhaseDone.String(),
		}, nil
	case FixVerdictNeedsInput:
		glog.V(2).Infof("build-fix planning: escalating repo=%s reason=%q", repo, plan.Reason)
		return needsInput(plan.Reason), nil
	default:
		glog.V(2).Infof("build-fix planning: verdict=%s repo=%s → execution", plan.Verdict, repo)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: domain.TaskPhaseExecution.String(),
		}, nil
	}
}

// setupFixWorkdir creates the ephemeral clone dir and registers its deferred
// cleanup on Run's stack.
func (s *fixPlanningStep) setupFixWorkdir(md *agentlib.Markdown) string {
	workdir := setupFixWorkdir(md)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf(
				"build-fix planning: workdir cleanup failed: path=%s err=%v",
				workdir,
				err,
			)
		}
	}()
	return workdir
}

// runDiagnosis verifies green-at-HEAD, fetches failed logs, and runs the
// Claude classification call. Returns the validated FixPlanOutput, or a
// terminal Result on clone/parse failure.
func (s *fixPlanningStep) runDiagnosis(
	ctx context.Context,
	md *agentlib.Markdown,
	repo, episodeSHA string,
) (*FixPlanOutput, *agentlib.Result) {
	// Fetch the failing-workflow log evidence. The body's ## Failing Workflows
	// table carries run URLs; fall back to `gh run view --log-failed` on the
	// latest failing run for the episode when the body is sparse.
	logEvidence := extractFailingWorkflowLogEvidence(ctx, md)
	if strings.TrimSpace(logEvidence) == "" && s.gh != nil {
		if r, err := s.gh.FetchFailedLogs(ctx, repo, episodeSHA); err == nil {
			logEvidence = r
		} else {
			glog.V(2).Infof("build-fix planning: gh log fetch failed repo=%s err=%v", repo, err)
		}
	}
	if strings.TrimSpace(logEvidence) == "" {
		return &FixPlanOutput{
			Verdict:    FixVerdictNeedsInput,
			Reason:     "no failing-workflow log evidence could be fetched for " + repo + " @" + episodeSHA + " — insufficient evidence to classify",
			EpisodeSHA: episodeSHA,
		}, nil
	}

	prompt := prompts.BuildFixDiagnosisPrompt(repo, episodeSHA, logEvidence)
	claudeResult, err := s.runner.Run(ctx, prompt)
	if err != nil {
		return nil, failed("build-fix diagnosis claude call failed: " + truncate(err.Error()))
	}

	plan, err := parseFixPlan(ctx, claudeResult.Result)
	if err != nil {
		return nil, failed("parse build-fix diagnosis output: " + truncate(err.Error()))
	}
	return plan, nil
}

// failFixClone builds the terminal clone/auth failure Result.
func (s *fixPlanningStep) failFixClone(repo string, err error) *agentlib.Result {
	return failed(fmt.Sprintf("build-fix clone failed for %s: %s", repo, truncate(err.Error())))
}
