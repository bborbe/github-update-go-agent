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

const planningTaskMD = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: planning
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-1
---

Update Go bborbe/demo
`

var _ = Describe("PlanningStep", func() {
	var (
		ctx    context.Context
		runner *mocks.ClaudeRunnerMock
		ops    *mocks.GitOps
		step   agentlib.Step
		md     *agentlib.Markdown
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		ops = &mocks.GitOps{}
		step = pkg.NewPlanningStep(runner, ops, "tok")
		var err error
		md, err = agentlib.ParseMarkdown(ctx, planningTaskMD)
		Expect(err).To(BeNil())
	})

	It("ShouldRun is always true", func() {
		should, err := step.ShouldRun(ctx, md)
		Expect(err).To(BeNil())
		Expect(should).To(BeTrue())
	})

	Describe("missing required frontmatter", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(
				ctx,
				"---\nassignee: github-update-go-agent\nrepo: bborbe/demo\nref: 6d1f27fabcdef\n---\n\nbody\n",
			)
			Expect(err).To(BeNil())
		})

		It("escalates NeedsInput naming the field, message only", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("clone_url"))
		})

		It("does not clone", func() {
			_, _ = step.Run(ctx, md)
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})

		It("never mutates assignee/status and never writes ## Failure", func() {
			_, _ = step.Run(ctx, md)
			_, hasFailure := md.FindSection("## Failure")
			Expect(hasFailure).To(BeFalse())
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-update-go-agent"))
			_, hasPrev := md.Frontmatter["previous_assignee"]
			Expect(hasPrev).To(BeFalse())
		})
	})

	Describe("clone auth failure", func() {
		BeforeEach(func() {
			ops.CloneAtRefReturns(stderrors.New("git clone: returned error: 403"))
		})

		It("fails with an App-installation hint", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("git auth failure"))
			Expect(result.Message).To(ContainSubstring("bborbe/demo"))
		})
	})

	Describe("happy path", func() {
		BeforeEach(func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"go_bump": {"from": "1.26.3", "to": "1.26.5"},
				"dep_updates_expected": true,
				"gate_targets": ["precommit", "check"],
				"vulns": [
					{"id": "GO-2026-1234", "package": "golang.org/x/text", "fixed_version": "v0.39.0", "scanner": "trivy", "action": "fix", "reason": "patched"}
				]
			}`}, nil)
		})

		It("clones the token-injected HTTPS URL at the ref", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(ops.CloneAtRefCallCount()).To(Equal(1))
			_, url, ref, _ := ops.CloneAtRefArgsForCall(0)
			Expect(url).To(Equal("https://x-access-token:tok@github.com/bborbe/demo.git"))
			Expect(ref).To(Equal("6d1f27fabcdef12345678901234567890abcdef1"))
		})

		It("writes a round-trippable ## Plan and advances to execution", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))

			plan, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(plan.Outcome).To(Equal(pkg.PlanOutcomeReady))
			Expect(plan.GateTargets).To(Equal([]string{"precommit", "check"}))
			Expect(plan.GoBump.To).To(Equal("1.26.5"))
		})
	})

	Describe("park path (design D4)", func() {
		BeforeEach(func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"gate_targets": ["check"],
				"vulns": [
					{"id": "GO-2026-5932", "package": "golang.org/x/crypto/openpgp", "scanner": "trivy", "action": "park", "reason": "no upstream fix"},
					{"id": "CVE-2026-9999", "scanner": "osv-scanner", "action": "park", "reason": "major bump required"}
				]
			}`}, nil)
		})

		It("parks NeedsInput naming finding IDs, scanners, and the 3 suppression files", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("GO-2026-5932"))
			Expect(result.Message).To(ContainSubstring("CVE-2026-9999"))
			Expect(result.Message).To(ContainSubstring("trivy"))
			Expect(result.Message).To(ContainSubstring("osv-scanner"))
			Expect(result.Message).To(ContainSubstring("VULNCHECK_IGNORE"))
			Expect(result.Message).To(ContainSubstring(".osv-scanner.toml"))
			Expect(result.Message).To(ContainSubstring(".trivyignore"))
		})

		It("still records the ## Plan for the operator", func() {
			_, _ = step.Run(ctx, md)
			plan, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(plan.Vulns).To(HaveLen(2))
		})

		It("never mutates assignee (controller owns the envelope)", func() {
			_, _ = step.Run(ctx, md)
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-update-go-agent"))
		})
	})

	Describe("no_update_needed", func() {
		BeforeEach(func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "no_update_needed",
				"has_work": false,
				"dep_updates_expected": false,
				"reason": "already on latest Go, gate clean"
			}`}, nil)
		})

		It("completes the task: Done + NextPhase done", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
		})
	})

	Describe("unparseable claude output", func() {
		BeforeEach(func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: "sorry, no json here"}, nil)
		})

		It("fails (controller retries)", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("parse planning output"))
		})
	})
})
