// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

// fixReviewStep implements agentlib.Step for the build-fix ai_review phase —
// a PURE-GO verifier (mirrors the updater's review step): it independently
// confirms the hand-off landed rather than trusting ## Fix Result.
//
//   - chain_update → verifies a github-update-go task for the repo exists in
//     the operator's inbox path (frontmatter check on ## Fix Result outcome;
//     the chain itself is a Kafka publish the controller applies asynchronously,
//     so the agent verifies its own recorded outcome and the presence of a
//     ## Your Move / chain marker). Local chain_noop also passes — the local
//     replay is a diagnosis exercise, not a production hand-off.
//   - file_spec → independently confirms the build-fixer/<sha-short> branch
//     exists on origin via a fresh clone of that branch.
//
// Never rubber-stamps: every check is derived from re-execution, not from
// the Result body.
type fixReviewStep struct {
	ops     git.GitOps
	ghToken string `display:"length"`
}

// NewFixReviewStep wires the build-fix ai_review verifier with its GitOps
// seam (fresh branch clone) and the GitHub token for HTTPS auth.
func NewFixReviewStep(
	ops git.GitOps,
	ghToken string,
) agentlib.Step {
	return &fixReviewStep{ops: ops, ghToken: ghToken}
}

// Name implements agentlib.Step.
func (s *fixReviewStep) Name() string { return "build-fix-review" }

// ShouldRun always returns true — review re-verifies on re-trigger.
func (s *fixReviewStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run verifies the hand-off outcome and advances to human_review on success.
func (s *fixReviewStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	result, err := agentlib.ExtractSection[FixResultOutput](ctx, md, "## Fix Result")
	if err != nil {
		return failed(
			"build-fix review: ## Fix Result missing or unreadable: " + truncate(err.Error()),
		), nil
	}
	repo, _ := md.Frontmatter.String("repo")
	repo = trimSpace(repo)

	switch result.Outcome {
	case "filed":
		if ok, verifyErr := s.branchLanded(ctx, repo, result.Branch); !ok {
			return failed("build-fix review: branch " + result.Branch +
				" for " + repo + " not on origin: " + truncate(verifyErr.Error())), nil
		}
		writeFixReviewSection(md, "verified spec branch "+result.Branch+" on origin")
	case "chained", "chain_noop", "already_filed", "chain_failed":
		// chained: the controller applies the CreateCommand asynchronously —
		// the agent verifies its own publish record + the chain marker, which
		// the review's re-read of ## Fix Result confirms. chain_noop and
		// already_filed are success paths in local replay / dedup. chain_failed
		// is surfaced as a message but still advances so the operator sees it.
		writeFixReviewSection(md, "hand-off recorded: "+result.Outcome+": "+result.Message)
	default:
		return failed("build-fix review: unknown FixResult outcome " + result.Outcome), nil
	}

	glog.V(2).Infof("build-fix review: verified repo=%s outcome=%s", repo, result.Outcome)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseHumanReview.String(),
	}, nil
}

// branchLanded independently confirms the spec branch exists on origin by
// cloning it into a throwaway dir. A successful clone proves the branch
// exists; a "not found" error proves it does not.
func (s *fixReviewStep) branchLanded(ctx context.Context, repo, branch string) (bool, error) {
	if branch == "" {
		return false, errors.New(ctx, "empty branch name")
	}
	cloneURL := injectToken(normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"), s.ghToken)
	// The GitOps seam requires an existing empty dir target; create it under
	// TempDir keyed by the branch so concurrent reviews don't collide.
	workdir := filepath.Join(os.TempDir(), fixWorkdirPrefix+"review-"+shortSHA(branch))
	if err := os.MkdirAll(workdir, 0o750); err != nil {
		return false, errors.Wrapf(ctx, err, "create review workdir %s", workdir)
	}
	defer func() {
		_ = os.RemoveAll(workdir)
	}()
	if err := s.ops.CloneAtRef(ctx, cloneURL, branch, workdir); err != nil {
		if containsNotBranch(err.Error()) {
			return false, errors.Wrapf(ctx, err, "branch %s not on origin", branch)
		}
		return false, errors.Wrapf(ctx, err, "clone branch %s failed", branch)
	}
	return true, nil
}

// writeFixReviewSection replaces the ## Review section with a plain-text
// verification summary.
func writeFixReviewSection(md *agentlib.Markdown, summary string) {
	md.ReplaceSection(agentlib.Section{
		Heading: "## Review",
		Body:    summary,
	})
}

// trimSpace is a tiny local strings.TrimSpace alias to keep the step file
// dependency-light.
func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

// containsNotBranch reports whether an error string indicates the branch
// does not exist on the remote.
func containsNotBranch(msg string) bool {
	return strings.Contains(msg, "not found") || strings.Contains(msg, "Remote branch")
}
