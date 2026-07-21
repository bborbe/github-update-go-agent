// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"

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

var _ = Describe("ReviewStep", func() {
	var (
		ctx  context.Context
		ops  *mocks.GitOps
		gh   *mocks.GhCli
		gate *mocks.GateRunner
		step agentlib.Step
		md   *agentlib.Markdown
	)

	BeforeEach(func() {
		ctx = context.Background()
		ops = &mocks.GitOps{}
		gh = &mocks.GhCli{}
		gate = &mocks.GateRunner{}
		step = pkg.NewReviewStep(ops, gh, gate, "tok")
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
		})

		It("verifies the fresh worktree at the branch, not the ref", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, _, ref, _ := ops.CloneAtRefArgsForCall(0)
			Expect(ref).To(Equal("fix/update-go-6d1f27f"))
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
			Expect(result.NextPhase).To(Equal(""))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Approved).To(BeFalse())
			Expect(review.Checks.PROpen).To(BeFalse())
		})
	})

	Describe("PR no longer draft", func() {
		BeforeEach(func() {
			gh.ViewPRReturns("OPEN", false, nil)
		})

		It("rejects with pr_draft false", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.PRDraft).To(BeFalse())
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

	Describe("tag leaked onto a branch commit", func() {
		BeforeEach(func() {
			ops.LsRemoteTagsReturns([]string{"deadbeef2"}, nil)
		})

		It("rejects with no_new_tag false", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			review, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
			Expect(err).To(BeNil())
			Expect(review.Checks.NoNewTag).To(BeFalse())
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
})
