// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

// changelogFileName is the changelog the review verifies.
const changelogFileName = "CHANGELOG.md"

// versionHeaderRegexp matches released version headers (## v1.2.3 / ## 1.2.3).
var versionHeaderRegexp = regexp.MustCompile(`(?m)^## v?\d+\.\d+\.\d+`)

// reviewStep implements agentlib.Step for the ai_review phase — a PURE-GO
// verifier (design D1): every check is derived from independent re-execution
// on a fresh worktree, never copied from ## Result, and a non-LLM verifier
// cannot rubber-stamp.
type reviewStep struct {
	ops     git.GitOps
	gh      GhCli
	gate    GateRunner
	ghToken string
}

// NewReviewStep wires the ai_review verifier with its GitOps seam (fresh
// clone + tag/rev inspection), gh CLI seam (PR state), gate runner
// (independent gate re-run), and the GitHub token.
func NewReviewStep(
	ops git.GitOps,
	gh GhCli,
	gate GateRunner,
	ghToken string,
) agentlib.Step {
	return &reviewStep{ops: ops, gh: gh, gate: gate, ghToken: ghToken}
}

// Name implements agentlib.Step.
func (s *reviewStep) Name() string { return "github-update-go-review" }

// ShouldRun always returns true — a re-trigger overwrites ## Review.
func (s *reviewStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run executes the verification pipeline (design § 4.3 ai_review):
//  1. Read ## Plan + ## Result (fatal error if either missing).
//  2. pr_open + pr_draft — `gh pr view` must report OPEN + draft.
//  3. Fresh worktree at the branch; re-run every planned gate target → exit 0
//     (gate_green; vulns_clear — the gate wraps the scanners).
//  4. changelog_unreleased — CHANGELOG has an ## Unreleased bullet and no
//     new version header vs master.
//  5. no_new_tag — `git ls-remote --tags` shows no tag at any branch commit.
//  6. All true → ## Review approved + Done/NextPhase human_review (the ONLY
//     writer of that phase; success semantics per doctrine).
//     Any false → ## Review approved:false + Status Failed, NO NextPhase.
func (s *reviewStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	result, err := agentlib.ExtractSection[ResultOutput](ctx, md, "## Result")
	if err != nil || result == nil {
		return nil, errors.Wrapf(ctx, err, "ai_review: extract ## Result section")
	}
	plan, err := agentlib.ExtractSection[PlanOutput](ctx, md, "## Plan")
	if err != nil || plan == nil {
		return nil, errors.Wrapf(ctx, err, "ai_review: extract ## Plan section")
	}

	var notes []string
	if result.PRURL == "" || result.Branch == "" {
		return s.finish(ctx, md, ReviewOutput{
			Approved: false,
			Notes:    "## Result carries no pr_url/branch — nothing to verify",
		})
	}

	checks := ReviewChecks{}
	s.checkPR(ctx, result, &checks, &notes)

	cloneURL, _ := md.Frontmatter.String("clone_url")
	ref, _ := md.Frontmatter.String("ref")
	repo, _ := md.Frontmatter.String("repo")
	authedURL := injectToken(normalizeCloneURLToHTTPS(cloneURL), s.ghToken)

	workdir := setupWorkdir(md, repo)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf("ai_review: workdir cleanup failed: path=%s err=%v", workdir, err)
		}
	}()

	if err := s.ops.CloneAtRef(ctx, authedURL, result.Branch, workdir); err != nil {
		// Fail closed: without the fresh worktree no local check can pass.
		notes = append(notes, "fresh worktree clone failed: "+git.RedactToken(err.Error()))
	} else {
		s.checkGates(ctx, workdir, plan, &checks, &notes)
		s.checkChangelog(ctx, workdir, &checks, &notes)
		s.checkNoNewTag(ctx, workdir, authedURL, ref, &checks, &notes)
	}

	approved := checks.PROpen && checks.PRDraft && checks.GateGreen &&
		checks.VulnsClear && checks.ChangelogUnreleased && checks.NoNewTag
	output := ReviewOutput{
		Approved: approved,
		Checks:   checks,
		Notes:    notesFor(notes),
	}
	return s.finish(ctx, md, output)
}

// finish writes ## Review and maps approved → Done/human_review,
// rejected → Failed with NO NextPhase (the controller parks; human_review
// is reserved for end-of-pipeline success).
func (s *reviewStep) finish(
	ctx context.Context,
	md *agentlib.Markdown,
	output ReviewOutput,
) (*agentlib.Result, error) {
	section, err := agentlib.MarshalSectionTyped(ctx, "## Review", output)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "marshal ## Review section")
	}
	md.ReplaceSection(section)

	if output.Approved {
		glog.V(2).Infof("ai_review: approved — routing human_review")
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: domain.TaskPhaseHumanReview.String(),
		}, nil
	}
	glog.V(2).Infof("ai_review: rejected — %s", output.Notes)
	return &agentlib.Result{
		Status:  agentlib.AgentStatusFailed,
		Message: output.Notes,
	}, nil
}

// checkPR verifies the PR is OPEN and still a draft (the agent never
// readies; a non-draft here means someone else flipped it — surfaced for
// the operator, and per the check contract the review fails closed).
func (s *reviewStep) checkPR(
	ctx context.Context,
	result *ResultOutput,
	checks *ReviewChecks,
	notes *[]string,
) {
	state, isDraft, err := s.gh.ViewPR(ctx, result.PRURL)
	if err != nil {
		*notes = append(*notes, "gh pr view failed: "+err.Error())
		return
	}
	checks.PROpen = state == "OPEN"
	checks.PRDraft = isDraft
	if !checks.PROpen {
		*notes = append(*notes, "pr state is "+state+", expected OPEN")
	}
	if !checks.PRDraft {
		*notes = append(*notes, "pr is not a draft")
	}
}

// checkGates independently re-runs every planned gate target on the fresh
// worktree. gate_green is derived from THIS re-execution, never from
// Result.gate_exit (design § 4.4). vulns_clear rides the same run — the
// repo's gate targets wrap the scanners (design § 7.3).
func (s *reviewStep) checkGates(
	ctx context.Context,
	workdir string,
	plan *PlanOutput,
	checks *ReviewChecks,
	notes *[]string,
) {
	if len(plan.GateTargets) == 0 {
		*notes = append(*notes, "plan carries no gate_targets")
		return
	}
	for _, target := range plan.GateTargets {
		tail, exitCode, err := s.gate.RunTarget(ctx, workdir, target)
		if err != nil {
			*notes = append(*notes, "gate target "+target+" red (exit "+
				strconv.Itoa(exitCode)+"): "+tail)
			return
		}
	}
	checks.GateGreen = true
	checks.VulnsClear = true
}

// checkChangelog verifies CHANGELOG.md on the branch carries at least one
// bullet under ## Unreleased and introduces NO new version header compared
// to origin/master (the release agent finalizes versions on merge — a new
// header here means the update finalized a version, which is forbidden).
func (s *reviewStep) checkChangelog(
	ctx context.Context,
	workdir string,
	checks *ReviewChecks,
	notes *[]string,
) {
	branchContent, err := os.ReadFile(
		filepath.Join(workdir, changelogFileName),
	) // #nosec G304 -- workdir is os.TempDir-rooted; filename is constant
	if err != nil {
		*notes = append(*notes, "read CHANGELOG.md failed: "+err.Error())
		return
	}
	if !hasUnreleasedBullet(string(branchContent)) {
		*notes = append(*notes, "CHANGELOG has no bullet under ## Unreleased")
		return
	}
	masterContent, err := s.ops.ShowFile(ctx, workdir, "origin/master", changelogFileName)
	if err != nil {
		*notes = append(*notes, "read master CHANGELOG failed: "+err.Error())
		return
	}
	masterHeaders := map[string]struct{}{}
	for _, h := range versionHeaderRegexp.FindAllString(string(masterContent), -1) {
		masterHeaders[h] = struct{}{}
	}
	for _, h := range versionHeaderRegexp.FindAllString(string(branchContent), -1) {
		if _, ok := masterHeaders[h]; !ok {
			*notes = append(*notes, "CHANGELOG introduces new version header "+h+
				" — versioning is the release agent's job on merge")
			return
		}
	}
	checks.ChangelogUnreleased = true
}

// checkNoNewTag verifies the remote holds no tag pointing at any branch
// commit (git ls-remote --tags unchanged with respect to the branch work).
func (s *reviewStep) checkNoNewTag(
	ctx context.Context,
	workdir, authedURL, ref string,
	checks *ReviewChecks,
	notes *[]string,
) {
	if ref == "" {
		*notes = append(*notes, "frontmatter ref missing — cannot compute branch commits")
		return
	}
	branchCommits, err := s.ops.RevList(ctx, workdir, ref)
	if err != nil {
		*notes = append(*notes, "rev-list failed: "+err.Error())
		return
	}
	tagSHAs, err := s.ops.LsRemoteTags(ctx, authedURL)
	if err != nil {
		*notes = append(*notes, "ls-remote --tags failed: "+git.RedactToken(err.Error()))
		return
	}
	commitSet := map[string]struct{}{}
	for _, sha := range branchCommits {
		commitSet[sha] = struct{}{}
	}
	for _, sha := range tagSHAs {
		if _, ok := commitSet[sha]; ok {
			*notes = append(*notes, "remote tag points at branch commit "+sha+
				" — a tag leaked from the update pipeline")
			return
		}
	}
	checks.NoNewTag = true
}

// hasUnreleasedBullet reports whether the ## Unreleased section contains at
// least one markdown bullet before the next ## heading.
func hasUnreleasedBullet(content string) bool {
	idx := strings.Index(content, "## Unreleased")
	if idx < 0 {
		return false
	}
	body := content[idx+len("## Unreleased"):]
	if next := strings.Index(body, "\n## "); next >= 0 {
		body = body[:next]
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			return true
		}
	}
	return false
}

// notesFor returns a human-readable one-liner naming each failure, or
// "all checks passed" on success.
func notesFor(notes []string) string {
	if len(notes) == 0 {
		return "all checks passed"
	}
	return strings.Join(notes, "; ")
}
