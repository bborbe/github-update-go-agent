// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

const reviewTaskMD = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-1
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "gate_exit": 0
}
` + "```" + `
`

// changelogMaster is the master CHANGELOG; changelogBranch adds only an
// Unreleased bullet (the compliant shape).
const changelogMaster = "# Changelog\n\n## Unreleased\n\n## v1.2.3\n\n- old release\n"

const changelogBranch = "# Changelog\n\n## Unreleased\n\n- update Go to 1.26.5 and update dependencies\n\n## v1.2.3\n\n- old release\n"

// reviewTaskMDGoBump is reviewTaskMD with a plan go_bump so the operator
// block proves from/to interpolation (spec AC4).
const reviewTaskMDGoBump = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-2
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "go_bump": {"from": "1.26.4", "to": "1.26.6"},
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "gate_exit": 0
}
` + "```" + `
`

// reviewTaskMDVuln is reviewTaskMD with a result carrying dep/vuln updates
// so the operator block proves count + ID interpolation (spec AC4).
const reviewTaskMDVuln = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-3
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "deps_updated": 3,
  "vulns_fixed": ["GO-2024-1234", "CVE-2025-1000"],
  "gate_exit": 0
}
` + "```" + `
`

// reviewTaskMDAdvisory is reviewTaskMD with a valid external advisory block in
// the frontmatter (spec 007). Its ## Plan deliberately carries NO vuln row for
// the advisory and its ## Result never mentions it, so a passing check can only
// have come from the frontmatter.
const reviewTaskMDAdvisory = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-advisory
advisory:
  id: GO-2026-7777
  package: example.com/indirect/dep
  fixed_version: v2.1.0
  source: osv
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "gate_exit": 0
}
` + "```" + `
`

// reviewTaskMDAdvisoryBadID carries an advisory whose ID matches none of the
// accepted shapes (GO-<year>-<n>, CVE-<year>-<n>, GHSA-xxxx-xxxx-xxxx).
const reviewTaskMDAdvisoryBadID = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-advisory-bad-id
advisory:
  id: NOT-AN-ADVISORY
  package: example.com/indirect/dep
  fixed_version: v2.1.0
  source: osv
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "gate_exit": 0
}
` + "```" + `
`

// reviewTaskMDAdvisoryMissingKey omits the required `source` key.
const reviewTaskMDAdvisoryMissingKey = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-advisory-missing-key
advisory:
  id: GO-2026-7777
  package: example.com/indirect/dep
  fixed_version: v2.1.0
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "gate_exit": 0
}
` + "```" + `
`

// advisoryFixtureGoMod is the worktree's module file for the AC8 row: the
// fixture root module plus one unrelated direct requirement. The advisory's
// package (example.com/indirect/dep) lives inside a module that is NOT a direct
// requirement, so this file names neither the package path nor the resolved
// version — only a module-graph lookup can produce the answer.
const advisoryFixtureGoMod = "module example.com/fixture\n\ngo 1.26.6\n\nrequire example.com/other v1.0.0\n"

var _ = Describe("ReviewStep", func() {
	var (
		ctx     context.Context
		ops     *mocks.GitOps
		gh      *mocks.GhCli
		gate    *mocks.GateRunner
		modules *mocks.ModuleResolver
		step    agentlib.Step
		md      *agentlib.Markdown
	)

	BeforeEach(func() {
		ctx = context.Background()
		ops = &mocks.GitOps{}
		gh = &mocks.GhCli{}
		gate = &mocks.GateRunner{}
		modules = &mocks.ModuleResolver{}
		step = pkg.NewReviewStep(ops, gh, gate, modules, "tok", pkg.PRTargetDraft)
		var err error
		md, err = agentlib.ParseMarkdown(ctx, reviewTaskMD)
		Expect(err).To(BeNil())

		// Happy-path fakes: PR open+draft; clone writes a compliant
		// CHANGELOG into the workdir; gates green; no tags at branch commits.
		gh.ViewPRReturns("OPEN", true, nil)
		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if err := os.MkdirAll(workdir, 0o750); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(workdir, "CHANGELOG.md"),
				[]byte(changelogBranch),
				0o600,
			)
		}
		ops.ShowFileReturns([]byte(changelogMaster), nil)
		gate.RunTargetReturns("", 0, nil)
		ops.RevListReturns([]string{"deadbeef1", "deadbeef2"}, nil)
		ops.LsRemoteTagsReturns([]string{"1111111", "2222222"}, nil)
	})

	Describe("happy path", func() {
		It("approves with all checks true and routes human_review", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeTrue())
			Expect(review.Checks.PROpen).To(BeTrue())
			Expect(review.Checks.PRDraft).To(BeTrue())
			Expect(review.Checks.GateGreen).To(BeTrue())
			Expect(review.Checks.VulnsClear).To(BeTrue())
			Expect(review.Checks.ChangelogUnreleased).To(BeTrue())
			Expect(review.Checks.NoNewTag).To(BeTrue())
			Expect(review.Notes).NotTo(ContainSubstring("draft-ness mismatch"))
		})

		It("verifies the fresh worktree at the branch, not the ref", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, _, ref, _ := ops.CloneAtRefArgsForCall(0)
			Expect(ref).To(Equal("fix/update-go-6d1f27f"))
		})

		It("bases the tag check on origin/master, the PR base", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, _, base := ops.RevListArgsForCall(0)
			Expect(base).To(Equal("origin/master"))
		})
	})

	Describe("Your Move operator-action block", func() {
		It("opens the approved body with the operator-action block above ## Plan", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))

			section, ok := md.FindSection("## Your Move")
			Expect(ok).To(BeTrue())
			Expect(section.Body).To(ContainSubstring(
				"[Open the PR](https://github.com/bborbe/demo/pull/42)",
			))
			Expect(section.Body).To(ContainSubstring("**Merge the PR** to apply the update."))
			Expect(section.Body).To(ContainSubstring("No version bump recorded"))
			Expect(section.Body).NotTo(ContainSubstring("{"))
			Expect(md.Sections[0].Heading).To(Equal("## Your Move"))
			Expect(md.Sections[1].Heading).To(Equal("## Plan"))
		})

		It("renders ## Your Move before ## Plan in the marshalled body", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			body, err := md.Marshal(ctx)
			Expect(err).To(BeNil())
			lines := strings.Split(body, "\n")
			yourMoveIdx := -1
			planIdx := -1
			for i, line := range lines {
				if line == "## Your Move" {
					yourMoveIdx = i
				}
				if line == "## Plan" {
					planIdx = i
				}
			}
			Expect(yourMoveIdx).To(BeNumerically(">=", 0))
			Expect(planIdx).To(BeNumerically(">=", 0))
			Expect(yourMoveIdx).To(BeNumerically("<", planIdx))
		})

		It("replaces the block in place on a re-run instead of duplicating it", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, err = step.Run(ctx, md)
			Expect(err).To(BeNil())
			count := 0
			for _, s := range md.Sections {
				if s.Heading == "## Your Move" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})
	})

	Describe("Your Move block with a Go version bump", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, reviewTaskMDGoBump)
			Expect(err).To(BeNil())
		})

		It("interpolates go_bump.from and go_bump.to", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			section, ok := md.FindSection("## Your Move")
			Expect(ok).To(BeTrue())
			Expect(section.Body).To(ContainSubstring("Go version bump: 1.26.4 → 1.26.6"))
		})
	})

	Describe("Your Move block with dependency and vulnerability updates", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, reviewTaskMDVuln)
			Expect(err).To(BeNil())
		})

		It("interpolates the dependency count and fixed-vulnerability IDs", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			section, ok := md.FindSection("## Your Move")
			Expect(ok).To(BeTrue())
			Expect(section.Body).To(ContainSubstring("Updated 3 dependencies"))
			Expect(
				section.Body,
			).To(ContainSubstring("Fixed vulnerabilities: GO-2024-1234, CVE-2025-1000"))
		})
	})

	Describe("PR not open", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("CLOSED", true, nil)
		})

		It("rejects: approved false + Failed + NO NextPhase", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(HavePrefix("ai_review: "))
			Expect(result.NextPhase).To(Equal(""))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Checks.PROpen).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("pr state is CLOSED, expected OPEN"))
		})

		It("writes no ## Your Move block on a rejected body", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, ok := md.FindSection("## Your Move")
			Expect(ok).To(BeFalse())
		})
	})

	Describe("PR already merged", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("MERGED", false, nil)
		})

		It("accepts the shipped PR as approved and routes done (auto-complete)", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeTrue())
			Expect(review.Checks.PROpen).To(BeFalse())
			Expect(review.Checks.PRDraft).To(BeFalse())
			Expect(review.Notes).NotTo(ContainSubstring("expected OPEN"))
			Expect(review.Notes).NotTo(ContainSubstring("draft-ness mismatch"))
		})

		It("writes no ## Your Move block when the PR already merged", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.NextPhase).To(Equal("done"))
			_, ok := md.FindSection("## Your Move")
			Expect(ok).To(BeFalse())
		})

		It("bypasses the draft check for the shipped state", func() {
			gh.ViewPRReturns("MERGED", true, nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeTrue())
			Expect(review.Notes).NotTo(ContainSubstring("draft-ness mismatch"))
		})
	})

	Describe("target draft, PR is ready", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("OPEN", false, nil)
		})

		It("rejects with mismatch note naming observed and configured state", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Checks.PRDraft).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("observed draft=false"))
			Expect(review.Notes).To(ContainSubstring("configured target=draft"))
		})
	})

	Describe("target ready, PR is ready", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("OPEN", false, nil)
			step = pkg.NewReviewStep(ops, gh, gate, modules, "tok", pkg.PRTargetReady)
		})

		It("approves and routes human_review with no mismatch note", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeTrue())
			Expect(review.Notes).NotTo(ContainSubstring("draft-ness mismatch"))
		})

		It("pr_draft reports raw observed value (false) while Approved is true", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.PRDraft).To(BeFalse())
			Expect(review.Approved).To(BeTrue())
		})
	})

	Describe("target ready, PR is draft", func() {
		BeforeEach(func() {
			// gh already returns true from the outer BeforeEach
			step = pkg.NewReviewStep(ops, gh, gate, modules, "tok", pkg.PRTargetReady)
		})

		It("rejects with mismatch note naming observed and configured state", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("observed draft=true"))
			Expect(review.Notes).To(ContainSubstring("configured target=ready"))
		})
	})

	Describe("ViewPR error", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("", false, stderrors.New("boom"))
		})

		It("rejects with Failed status and gh pr view failed note", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("gh pr view failed"))
		})
	})

	Describe("gate red on re-run", func() {
		BeforeEach(func() {
			gate.RunTargetReturns("test failure tail", 2, stderrors.New("make precommit failed"))
		})

		It("rejects with gate_green + vulns_clear false and the failing target in notes", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.GateGreen).To(BeFalse())
			Expect(review.Checks.VulnsClear).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("precommit"))
		})
	})

	Describe("CHANGELOG without Unreleased bullet", func() {
		BeforeEach(func() {
			ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
				if err := os.MkdirAll(workdir, 0o750); err != nil {
					return err
				}
				return os.WriteFile(
					filepath.Join(workdir, "CHANGELOG.md"),
					[]byte(changelogMaster),
					0o600,
				)
			}
		})

		It("rejects with changelog_unreleased false", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.ChangelogUnreleased).To(BeFalse())
		})
	})

	Describe("CHANGELOG finalized a new version header", func() {
		BeforeEach(func() {
			finalized := "# Changelog\n\n## Unreleased\n\n- pending\n\n## v1.3.0\n\n- update deps\n\n## v1.2.3\n\n- old release\n"
			ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
				if err := os.MkdirAll(workdir, 0o750); err != nil {
					return err
				}
				return os.WriteFile(
					filepath.Join(workdir, "CHANGELOG.md"),
					[]byte(finalized),
					0o600,
				)
			}
		})

		It("rejects — versioning is the release agent's job", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.ChangelogUnreleased).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("v1.3.0"))
		})
	})

	Describe("tag reachable from the base branch (release history)", func() {
		BeforeEach(func() {
			ops.RevListStub = func(_ context.Context, _, base string) ([]string, error) {
				// The stale pinned ref would include master commits added after
				// filing (e.g. a release commit); origin/master (the branch's
				// actual base) yields only the branch's own commits.
				if base == "origin/master" {
					return []string{"f8b922c2"}, nil
				}
				return []string{"f8b922c2", "6e16a948"}, nil
			}
			ops.LsRemoteTagsReturns([]string{"6e16a948"}, nil)
		})

		It("excludes a release tag on master history and bases RevList on origin/master", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.NoNewTag).To(BeTrue())
			Expect(review.Notes).NotTo(ContainSubstring("tag leaked"))
			_, _, base := ops.RevListArgsForCall(0)
			Expect(base).To(Equal("origin/master"))
		})
	})

	Describe("tag leaked onto a branch commit", func() {
		BeforeEach(func() {
			ops.LsRemoteTagsReturns([]string{"deadbeef2"}, nil)
		})

		It("rejects with no_new_tag false and the genuine-leak note", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.NoNewTag).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("tag leaked"))
		})
	})

	Describe("repro: release-tag-in-history + already-merged PR (spec 005)", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("MERGED", false, nil)
			ops.RevListReturns([]string{"f8b922c2"}, nil)      // the branch's own commit
			ops.LsRemoteTagsReturns([]string{"6e16a948"}, nil) // legitimate release tag on master
		})

		It("approves the merged, clean-and-shipped PR and routes done (auto-complete)", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeTrue())
			Expect(review.Notes).NotTo(ContainSubstring("tag leaked"))
			Expect(review.Notes).NotTo(ContainSubstring("expected OPEN"))
		})
	})

	Describe("rev-list / ls-remote failure", func() {
		It("keeps NoNewTag false when git rev-list fails", func() {
			ops.RevListReturns(nil, stderrors.New("rev-list boom"))
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, _ := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(review.Checks.NoNewTag).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("rev-list failed"))
		})

		It("keeps NoNewTag false when git ls-remote --tags fails", func() {
			ops.LsRemoteTagsReturns(nil, stderrors.New("ls-remote boom"))
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, _ := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(review.Checks.NoNewTag).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("ls-remote --tags failed"))
		})
	})

	Describe("missing ## Result", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, "---\nrepo: bborbe/demo\n---\n\nbody\n")
			Expect(err).To(BeNil())
		})

		It("returns a wrapped error (framework handles)", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("external advisory verification", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, reviewTaskMDAdvisory)
			Expect(err).To(BeNil())
		})

		It("AC3: rejects an advisory below its fixed version despite a green gate", func() {
			modules.ModulesReturns([]pkg.ModuleVersion{
				{Path: "example.com/indirect", Version: "v2.0.0"},
			}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(HavePrefix("ai_review: "))
			Expect(result.NextPhase).To(Equal(""))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.GateGreen).To(BeTrue())
			Expect(review.Checks.VulnsClear).To(BeFalse())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("GO-2026-7777"))
			Expect(review.Notes).To(ContainSubstring("example.com/indirect/dep"))
			Expect(review.Notes).To(ContainSubstring("v2.0.0"))
			Expect(review.Notes).To(ContainSubstring("v2.1.0"))
		})

		It("AC3: writes no ## Your Move block on an un-cleared advisory", func() {
			modules.ModulesReturns([]pkg.ModuleVersion{
				{Path: "example.com/indirect", Version: "v2.0.0"},
			}, nil)

			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, ok := md.FindSection("## Your Move")
			Expect(ok).To(BeFalse())

			body, err := md.Marshal(ctx)
			Expect(err).To(BeNil())
			for _, line := range strings.Split(body, "\n") {
				Expect(line).NotTo(Equal("## Your Move"))
			}
		})

		It(
			"AC3 mirror: approves when the installed version is at or above the fixed version",
			func() {
				modules.ModulesReturns([]pkg.ModuleVersion{
					{Path: "example.com/indirect", Version: "v2.1.0"},
				}, nil)

				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("human_review"))

				review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
				Expect(err).To(BeNil())
				Expect(review.Checks.VulnsClear).To(BeTrue())
				Expect(review.Approved).To(BeTrue())
				Expect(review.Notes).NotTo(ContainSubstring("GO-2026-7777"))
			},
		)

		It("DB7: a satisfied advisory never certifies a red gate", func() {
			gate.RunTargetReturns("test failure tail", 2, stderrors.New("make precommit failed"))
			modules.ModulesReturns([]pkg.ModuleVersion{
				{Path: "example.com/indirect", Version: "v2.3.0"},
			}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.GateGreen).To(BeFalse())
			Expect(review.Checks.VulnsClear).To(BeFalse())
			Expect(review.Approved).To(BeFalse())
		})

		It("AC7: keys on the frontmatter, not on the plan or the result", func() {
			// The fixture's ## Plan carries no vuln row for the advisory and its
			// ## Result never mentions it — the check still fires.
			body, err := md.Marshal(ctx)
			Expect(err).To(BeNil())
			planSection, ok := md.FindSection("## Plan")
			Expect(ok).To(BeTrue())
			Expect(planSection.Body).NotTo(ContainSubstring("GO-2026-7777"))
			Expect(body).NotTo(ContainSubstring(`"vulns"`))

			modules.ModulesReturns([]pkg.ModuleVersion{
				{Path: "example.com/indirect", Version: "v2.0.0"},
			}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.VulnsClear).To(BeFalse())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("GO-2026-7777"))
		})

		It("AC7: fails closed on a block whose ID is outside the accepted shapes", func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, reviewTaskMDAdvisoryBadID)
			Expect(err).To(BeNil())

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.VulnsClear).To(BeFalse())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("field=id"))
			Expect(review.Notes).To(ContainSubstring("NOT-AN-ADVISORY"))
			// The resolver is never consulted for an invalid block.
			Expect(modules.ModulesCallCount()).To(Equal(0))
		})

		It("AC7: fails closed on a block missing a required key", func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, reviewTaskMDAdvisoryMissingKey)
			Expect(err).To(BeNil())

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.VulnsClear).To(BeFalse())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Notes).To(ContainSubstring("field=source"))
			Expect(review.Notes).To(ContainSubstring("<nil>"))
		})

		It("AC8: resolves a package that lives only inside an indirect module", func() {
			ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
				if err := os.MkdirAll(workdir, 0o750); err != nil {
					return err
				}
				if err := os.WriteFile(
					filepath.Join(workdir, "CHANGELOG.md"),
					[]byte(changelogBranch),
					0o600,
				); err != nil {
					return err
				}
				return os.WriteFile(
					filepath.Join(workdir, "go.mod"),
					[]byte(advisoryFixtureGoMod),
					0o600,
				)
			}
			modules.ModulesReturns([]pkg.ModuleVersion{
				{Path: "example.com/fixture"},
				{Path: "example.com/other", Version: "v1.0.0"},
				{Path: "example.com/indirect", Version: "v2.3.0"},
			}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.VulnsClear).To(BeTrue())
			Expect(review.Approved).To(BeTrue())

			// No go.mod text read can produce the answer: the package path is
			// not in the fixture's module file, and neither is the resolved
			// version.
			Expect(advisoryFixtureGoMod).NotTo(ContainSubstring("example.com/indirect/dep"))
			Expect(advisoryFixtureGoMod).NotTo(ContainSubstring("v2.3.0"))
		})

		DescribeTable("AC8: fails closed when the installed version is undeterminable",
			func(installed []pkg.ModuleVersion, resolveErrMsg, reason string) {
				var resolveErr error
				if resolveErrMsg != "" {
					resolveErr = stderrors.New(resolveErrMsg)
				}
				modules.ModulesReturns(installed, resolveErr)

				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))

				review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
				Expect(err).To(BeNil())
				Expect(review.Checks.VulnsClear).To(BeFalse())
				Expect(review.Approved).To(BeFalse())
				Expect(review.Notes).To(ContainSubstring("GO-2026-7777"))
				Expect(review.Notes).To(ContainSubstring("example.com/indirect/dep"))
				Expect(review.Notes).To(ContainSubstring(reason))
			},
			Entry(
				"package absent from the module graph",
				[]pkg.ModuleVersion{{Path: "example.com/other", Version: "v2.3.0"}},
				"",
				"is not in the module graph",
			),
			Entry(
				"unparseable installed version",
				[]pkg.ModuleVersion{{Path: "example.com/indirect", Version: "v0.1.0-"}},
				"",
				"unparseable installed version",
			),
			Entry(
				"resolution command failure",
				[]pkg.ModuleVersion(nil),
				"go list -m all exceeded 2m0s",
				"module graph resolution for package example.com/indirect/dep failed",
			),
		)

		When("the task carries no advisory block", func() {
			BeforeEach(func() {
				var err error
				md, err = agentlib.ParseMarkdown(ctx, reviewTaskMD)
				Expect(err).To(BeNil())
			})

			It("AC8: never consults the resolver and rides the gate re-run", func() {
				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

				review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
				Expect(err).To(BeNil())
				Expect(review.Checks.VulnsClear).To(BeTrue())
				Expect(review.Approved).To(BeTrue())
				Expect(modules.ModulesCallCount()).To(Equal(0))
			})
		})
	})
})
