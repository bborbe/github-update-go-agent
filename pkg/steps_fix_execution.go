// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

// fixBranchPrefix is the deterministic spec branch prefix; the full branch is
// fixBranchPrefix + episodeSha[:7]. Determinism is the dedup guard: a re-run
// computes the same branch, the git ls-remote pre-check sees it, and skips
// (design: episode-SHA semantics).
const fixBranchPrefix = "build-fixer/"

// bugSpecDir is where build-fix files kind:bug specs in the target repo.
const bugSpecDir = "specs/ideas/"

// bugSpecFilenamePrefix is the deterministic spec filename prefix.
const bugSpecFilenamePrefix = "bug-build-failure-"

// fixExecutionStep implements agentlib.Step for the build-fix execution
// phase — the deterministic hand-off, in Go, no LLM:
//
//   - verdict chain_update → publish a github-update-go task (CreateCommand)
//     chaining to the updater agent, and write ## Fix Result.
//   - verdict file_spec    → clone target, switch build-fixer/<sha-short>,
//     write the kind:bug spec under specs/ideas/, commit, push; git
//     ls-remote dedup pre-check before any work.
//
// The spec content is built from ## Fix Plan (the diagnosis verdict + reason)
// plus the task's failing-workflow evidence — deterministic, no LLM, so the
// spec cannot drift from the diagnosis. This keeps the fixer lean: it is a
// hand-off, not a fixer (dark-factory fixes code bugs; the updater fixes
// dep/vuln).
type fixExecutionStep struct {
	ops       git.GitOps
	gh        GhCli
	ghToken   string `display:"length"`
	createCmd CreateCommandFunc
}

// CreateCommandFunc publishes a downstream task (chain emission). The agent
// cannot write vault files directly; the controller's Kafka command bus is
// the only sanctioned producer path (watchers use task.CreateCommandSender).
// main.go wires the real implementation; nil in local CLI mode → the step
// logs the intended chain and completes (no-op) so the operator can replay
// the diagnosis locally without a cluster.
type CreateCommandFunc func(ctx context.Context, repo, episodeSHA string, workflows []string) error

// NewFixExecutionStep wires the build-fix execution step with its GitOps
// seam (clone/branch/commit/push), gh CLI seam, the GitHub token, and the
// chain-emission function (nil disables chain publishing).
func NewFixExecutionStep(
	ops git.GitOps,
	gh GhCli,
	ghToken string,
	createCmd CreateCommandFunc,
) agentlib.Step {
	return &fixExecutionStep{ops: ops, gh: gh, ghToken: ghToken, createCmd: createCmd}
}

// Name implements agentlib.Step.
func (s *fixExecutionStep) Name() string { return "build-fix-execute" }

// ShouldRun always returns true; the replay guard lives inside Run so a
// crash-window replay can re-route without redoing side effects.
func (s *fixExecutionStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run executes the build-fix hand-off per the ## Fix Plan verdict:
//  1. Replay guard — existing successful ## Fix Result → re-route to ai_review.
//  2. Read ## Fix Plan (must show a valid verdict).
//  3. chain_update → publish the github-update-go CreateCommand (or log the
//     no-op locally) → ## Fix Result.
//  4. file_spec → dedup pre-check, clone, branch, write kind:bug spec,
//     commit, push → ## Fix Result.
//  5. no_fix_needed / needs_input → terminal failed (should never reach
//     execution; planning already closed/escalated).
func (s *fixExecutionStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	if _, err := agentlib.ExtractSection[FixResultOutput](ctx, md, "## Fix Result"); err == nil {
		glog.V(2).
			Infof("build-fix execution: replay — ## Fix Result present, re-routing to ai_review")
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: domain.TaskPhaseAIReview.String(),
		}, nil
	}

	plan, err := extractFixPlan(ctx, md)
	if err != nil {
		return failed(
			"build-fix execution: ## Fix Plan missing or unreadable: " + truncate(err.Error()),
		), nil
	}

	repo, _ := md.Frontmatter.String("repo")
	repo = strings.TrimSpace(repo)

	var result *FixResultOutput
	switch plan.Verdict {
	case FixVerdictChainUpdate:
		result = s.chainToUpdater(ctx, repo, plan)
	case FixVerdictFileSpec:
		result = s.fileSpec(ctx, md, repo, plan)
	default:
		glog.V(2).
			Infof("build-fix execution: unexpected verdict=%q — should have closed in planning", plan.Verdict)
		return failed(
			fmt.Sprintf(
				"build-fix execution: unexpected verdict %q — planning should have closed/escalated",
				plan.Verdict,
			),
		), nil
	}

	section, err := agentlib.MarshalSectionTyped(ctx, "## Fix Result", *result)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Fix Result section")
	}
	md.ReplaceSection(section)
	glog.V(2).Infof("build-fix execution: repo=%s outcome=%s", repo, result.Outcome)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseAIReview.String(),
	}, nil
}

// chainToUpdater emits a github-update-go task for the repo (the dep/vuln
// path). When createCmd is nil (local replay), it records the intended chain
// as a no-op so the local diagnosis can still be exercised end-to-end.
func (s *fixExecutionStep) chainToUpdater(
	ctx context.Context,
	repo string,
	plan *FixPlanOutput,
) *FixResultOutput {
	if s.createCmd == nil {
		glog.V(2).
			Infof("build-fix execution: createCmd nil (local mode) — recording chain no-op repo=%s", repo)
		return &FixResultOutput{
			Outcome: "chain_noop",
			Message: "would emit github-update-go task for " + repo +
				" (chain disabled in local mode): " + plan.Reason,
			Branch: "",
		}
	}
	if err := s.createCmd(ctx, repo, plan.EpisodeSHA, plan.FailingWorkflows); err != nil {
		glog.V(2).Infof("build-fix execution: chain emit failed repo=%s err=%v", repo, err)
		return &FixResultOutput{
			Outcome: "chain_failed",
			Message: "failed to emit github-update-go task for " + repo + ": " + truncate(
				err.Error(),
			),
			Branch: "",
		}
	}
	return &FixResultOutput{
		Outcome: "chained",
		Message: "emitted github-update-go task for " + repo + ": " + plan.Reason,
		Branch:  "",
	}
}

// fileSpec files the kind:bug spec for a code/test bug. Full deterministic
// pipeline: dedup pre-check → clone → branch → write spec → commit → push.
func (s *fixExecutionStep) fileSpec(
	ctx context.Context,
	md *agentlib.Markdown,
	repo string,
	plan *FixPlanOutput,
) *FixResultOutput {
	branch := fixBranchPrefix + shortSHA(plan.EpisodeSHA)
	workdir := setupFixWorkdir(md)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf(
				"build-fix execution: workdir cleanup failed: path=%s err=%v",
				workdir,
				err,
			)
		}
	}()

	// Dedup pre-check: an existing build-fixer/<sha-short> branch on origin
	// means an earlier run already filed this episode's spec. Skip.
	if s.branchExistsOnOrigin(ctx, repo, branch) {
		glog.V(2).Infof("build-fix execution: branch %s already on origin — skip (dedup)", branch)
		return &FixResultOutput{
			Outcome: "already_filed",
			Message: "spec for episode " + plan.EpisodeSHA + " already filed on " + branch,
			Branch:  branch,
		}
	}

	cloneURL := injectToken(normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"), s.ghToken)
	if err := s.ops.CloneAtRef(ctx, cloneURL, "HEAD", workdir); err != nil {
		return s.failFix(repo, branch, "clone", err)
	}
	if err := s.ops.SwitchNewBranch(ctx, workdir, branch); err != nil {
		return s.failFix(repo, branch, "branch switch", err)
	}

	if failResult := s.writeSpecFile(ctx, md, repo, plan, workdir); failResult != nil {
		return failResult
	}
	if failResult := s.commitAndPushSpec(ctx, repo, plan, workdir, branch); failResult != nil {
		return failResult
	}
	return &FixResultOutput{
		Outcome: "filed",
		Message: "filed kind:bug spec on " + branch + " for episode " + plan.EpisodeSHA,
		Branch:  branch,
	}
}

// writeSpecFile writes the kind:bug spec under specs/ideas/ in the workdir.
func (s *fixExecutionStep) writeSpecFile(
	ctx context.Context,
	md *agentlib.Markdown,
	repo string,
	plan *FixPlanOutput,
	workdir string,
) *FixResultOutput {
	branch := fixBranchPrefix + shortSHA(plan.EpisodeSHA)
	specPath := filepath.Join(
		workdir,
		bugSpecDir,
		bugSpecFilenamePrefix+shortSHA(plan.EpisodeSHA)+".md",
	)
	if err := os.MkdirAll(filepath.Dir(specPath), 0o750); err != nil {
		return s.failFix(repo, branch, "create specs/ideas dir", err)
	}
	spec := BuildBugSpec(repo, plan, extractFailingWorkflowLogEvidence(ctx, md))
	if err := os.WriteFile(specPath, []byte(spec), 0o600); err != nil {
		return s.failFix(repo, branch, "write bug spec", err)
	}
	return nil
}

// commitAndPushSpec commits the spec file and pushes the branch.
func (s *fixExecutionStep) commitAndPushSpec(
	ctx context.Context,
	repo string,
	plan *FixPlanOutput,
	workdir, branch string,
) *FixResultOutput {
	specPath := filepath.Join(
		workdir,
		bugSpecDir,
		bugSpecFilenamePrefix+shortSHA(plan.EpisodeSHA)+".md",
	)
	rel := filepath.ToSlash(strings.TrimPrefix(specPath, workdir+string(os.PathSeparator)))
	if _, err := s.ops.Commit(ctx, workdir, "Add bug spec for build failure "+shortSHA(plan.EpisodeSHA), rel); err != nil {
		return s.failFix(repo, branch, "commit bug spec", err)
	}
	if err := s.ops.Push(ctx, workdir, branch); err != nil {
		return s.failFix(repo, branch, "push bug spec", err)
	}
	return nil
}

// failFix builds a failed FixResultOutput for a named pipeline step.
func (s *fixExecutionStep) failFix(repo, branch, step string, err error) *FixResultOutput {
	return &FixResultOutput{
		Outcome: "failed",
		Message: step + " failed for " + repo + ": " + truncate(err.Error()),
		Branch:  branch,
	}
}

// branchExistsOnOrigin is a cheap dedup pre-check: does the spec branch
// already exist on origin? Uses git ls-remote via the GitOps seam's clone
// capability; returns false on any error (the subsequent push will surface a
// real conflict).
func (s *fixExecutionStep) branchExistsOnOrigin(ctx context.Context, repo, branch string) bool {
	// The GitOps seam has no ls-remote method; a bare clone at the branch ref
	// failing with "not found" is the signal the branch does NOT exist, while
	// a successful clone proves it does. Cheap, no side effects (clone only).
	cloneURL := injectToken(normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"), s.ghToken)
	tmp := filepath.Join(os.TempDir(), fixWorkdirPrefix+"-dedup-"+shortSHA(branch))
	defer func() {
		_ = os.RemoveAll(tmp)
	}()
	err := s.ops.CloneAtRef(ctx, cloneURL, branch, tmp)
	if err == nil {
		return true
	}
	if strings.Contains(err.Error(), "not found") ||
		strings.Contains(err.Error(), "Remote branch") {
		return false
	}
	// Unknown error — assume exists to be safe (avoid duplicate pushes).
	return true
}

// shortSHA bounds an episode SHA (or any long id) to its 7-char prefix.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// FixResultOutput is the typed contract for the `## Fix Result` section.
type FixResultOutput struct {
	Outcome string `json:"outcome"`
	Message string `json:"message,omitempty"`
	Branch  string `json:"branch,omitempty"`
}
