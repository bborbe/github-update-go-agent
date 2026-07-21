// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

const executionTaskMD = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: execution
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
  "go_bump": {"from": "1.26.3", "to": "1.26.5"},
  "dep_updates_expected": true,
  "gate_targets": ["precommit", "check"],
  "vulns": [
    {"id": "GO-2026-1234", "action": "fix", "reason": "patched"},
    {"id": "GO-2026-9999", "action": "fix", "reason": "patched"}
  ]
}
` + "```" + `
`

var _ = Describe("ExecutionStep", func() {
	var (
		ctx    context.Context
		runner *mocks.ClaudeRunnerMock
		ops    *mocks.GitOps
		gh     *mocks.GhCli
		gate   *mocks.GateRunner
		step   agentlib.Step
		md     *agentlib.Markdown
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		ops = &mocks.GitOps{}
		gh = &mocks.GhCli{}
		gate = &mocks.GateRunner{}
		step = pkg.NewExecutionStep(runner, ops, gh, gate, "tok")
		var err error
		md, err = agentlib.ParseMarkdown(ctx, executionTaskMD)
		Expect(err).To(BeNil())

		runner.RunReturns(&claudelib.ClaudeResult{
			Result: `{"deps_updated": 5, "vulns_fixed": ["GO-2026-1234"], "notes": "ok"}`,
		}, nil)
		gate.RunTargetReturns("", 0, nil)
		ops.ChangedFilesReturns([]string{"go.mod", "go.sum", "CHANGELOG.md"}, nil)
		ops.CommitReturns("abc1234", nil)
		ops.CommittedFilesReturns([]string{"go.mod", "go.sum", "CHANGELOG.md"}, nil)
		gh.CreateDraftPRReturns("https://github.com/bborbe/demo/pull/42", nil)
	})

	Describe("replay guard", func() {
		BeforeEach(func() {
			section, err := agentlib.MarshalSectionTyped(ctx, "## Result", pkg.ResultOutput{
				Outcome: pkg.ResultOutcomeOpened,
				Branch:  "fix/update-go-6d1f27f",
				PRURL:   "https://github.com/bborbe/demo/pull/42",
			})
			Expect(err).To(BeNil())
			md.ReplaceSection(section)
		})

		It("re-routes to ai_review without redoing side effects", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
			Expect(ops.PushCallCount()).To(Equal(0))
			Expect(gh.CreateDraftPRCallCount()).To(Equal(0))
		})
	})

	Describe("failed prior ## Result does NOT re-route", func() {
		BeforeEach(func() {
			section, err := agentlib.MarshalSectionTyped(ctx, "## Result", pkg.ResultOutput{
				Outcome: pkg.ResultOutcomeFailed,
				Error:   "gate red",
			})
			Expect(err).To(BeNil())
			md.ReplaceSection(section)
		})

		It("re-runs the pipeline", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(ops.CloneAtRefCallCount()).To(Equal(1))
		})
	})

	Describe("PR adopt guard (crash window)", func() {
		BeforeEach(func() {
			gh.FindOpenPRByHeadReturns("https://github.com/bborbe/demo/pull/41", nil)
		})

		It("adopts the open PR and writes ## Result without pushing", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))
			Expect(ops.PushCallCount()).To(Equal(0))

			out, err := agentlib.ExtractSection[pkg.ResultOutput](ctx, md, "## Result")
			Expect(err).To(BeNil())
			Expect(out.Outcome).To(Equal(pkg.ResultOutcomeAdopted))
			Expect(out.Branch).To(Equal("fix/update-go-6d1f27f"))
			Expect(out.PRURL).To(Equal("https://github.com/bborbe/demo/pull/41"))
		})
	})

	Describe("happy path", func() {
		It("clones at ref, branches deterministically, pushes, opens a draft PR", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("ai_review"))

			_, url, ref, _ := ops.CloneAtRefArgsForCall(0)
			Expect(url).To(Equal("https://x-access-token:tok@github.com/bborbe/demo.git"))
			Expect(ref).To(Equal("6d1f27fabcdef12345678901234567890abcdef1"))

			_, _, branch := ops.SwitchNewBranchArgsForCall(0)
			Expect(branch).To(Equal("fix/update-go-6d1f27f"))

			_, _, pushBranch := ops.PushArgsForCall(0)
			Expect(pushBranch).To(Equal("fix/update-go-6d1f27f"))

			_, _, base, head, title, _ := gh.CreateDraftPRArgsForCall(0)
			Expect(base).To(Equal("master"))
			Expect(head).To(Equal("fix/update-go-6d1f27f"))
			Expect(title).To(Equal("update go module dependencies"))
		})

		It("re-runs every planned gate target", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(gate.RunTargetCallCount()).To(Equal(2))
			_, _, t0 := gate.RunTargetArgsForCall(0)
			_, _, t1 := gate.RunTargetArgsForCall(1)
			Expect([]string{t0, t1}).To(Equal([]string{"precommit", "check"}))
		})

		It("commits the changed files with an explicit pathspec", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(ops.CommitCallCount()).To(Equal(1))
			_, _, message, paths := ops.CommitArgsForCall(0)
			Expect(message).To(Equal("update go module dependencies"))
			Expect(paths).To(Equal([]string{"go.mod", "go.sum", "CHANGELOG.md"}))
		})

		It("writes a round-trippable ## Result with the vulns-fixed invariant", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			out, err := agentlib.ExtractSection[pkg.ResultOutput](ctx, md, "## Result")
			Expect(err).To(BeNil())
			Expect(out.Outcome).To(Equal(pkg.ResultOutcomeOpened))
			Expect(out.Branch).To(Equal("fix/update-go-6d1f27f"))
			Expect(out.PRURL).To(Equal("https://github.com/bborbe/demo/pull/42"))
			Expect(out.GateExit).To(Equal(0))
			Expect(out.DepsUpdated).To(Equal(5))
			// invariant: subset of plan fix-action ids
			Expect(out.VulnsFixed).To(Equal([]string{"GO-2026-1234"}))
		})
	})

	Describe("red gate after claude", func() {
		BeforeEach(func() {
			gate.RunTargetReturnsOnCall(0, "", 0, nil)
			gate.RunTargetReturnsOnCall(1, "trivy found CVE-X", 2, stderrors.New("make check failed"))
		})

		It("fails with the failing target + output tail; no commit, no push, no PR", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring(`gate target "check" failed`))
			Expect(result.Message).To(ContainSubstring("trivy found CVE-X"))
			Expect(ops.CommitCallCount()).To(Equal(0))
			Expect(ops.PushCallCount()).To(Equal(0))
			Expect(gh.CreateDraftPRCallCount()).To(Equal(0))

			out, rerr := agentlib.ExtractSection[pkg.ResultOutput](ctx, md, "## Result")
			Expect(rerr).To(BeNil())
			Expect(out.Outcome).To(Equal(pkg.ResultOutcomeFailed))
			Expect(out.FailedTarget).To(Equal("check"))
			Expect(out.GateExit).To(Equal(2))
		})
	})

	Describe("workflow-edit guard", func() {
		BeforeEach(func() {
			ops.ChangedFilesReturns(
				[]string{"go.mod", ".github/workflows/ci.yml"},
				nil,
			)
		})

		It("refuses to commit changes under .github/workflows/", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring(".github/workflows/ci.yml"))
			Expect(ops.CommitCallCount()).To(Equal(0))
			Expect(ops.PushCallCount()).To(Equal(0))
		})
	})

	Describe("missing ## Plan", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(ctx, `---
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
---

body
`)
			Expect(err).To(BeNil())
		})

		It("fails without side effects", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})
	})
})
