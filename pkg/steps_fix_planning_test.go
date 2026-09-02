// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("build-fix planning diagnosis", func() {
	buildStep := func(runner claudelib.ClaudeRunner, gh updatepkg.GhCli) agentlib.Step {
		return updatepkg.NewFixPlanningStep(runner, nil, gh, "")
	}

	It("parses a prose-prefixed JSON verdict instead of escalating", func() {
		runner := &mocks.ClaudeRunnerMock{}
		runner.RunReturns(
			&claudelib.ClaudeResult{
				Result: "Based on the collected evidence, I can now make a determination:\n" +
					`{"verdict":"chain_update","reason":"stale dependency","episode_sha":"c052eef"}`,
			},
			nil,
		)
		gh := &mocks.GhCli{}
		gh.FetchFailedLogsReturns("failing workflow log evidence", nil)
		step := buildStep(runner, gh)

		plan, result := updatepkg.RunDiagnosisForTest(
			step,
			&agentlib.Markdown{},
			"bborbe/http",
			"c052eef",
		)
		Expect(result).To(BeNil())
		Expect(plan).ToNot(BeNil())
		Expect(plan.Verdict).To(Equal(updatepkg.FixVerdictChainUpdate))
		Expect(plan.EpisodeSHA).To(Equal("c052eef"))
	})

	// Regression for the 2026-09-02 c052eef incident: the model's final message
	// was prose-only ("Based on the collected evidence... Let me synthesize:")
	// with NO recoverable JSON, which previously crashed parseFixPlan into
	// ## Failure with an opaque unmarshal error. It must escalate to
	// needs_input with a readable reason instead.
	It("escalates to needs_input when the model returns prose-only output", func() {
		runner := &mocks.ClaudeRunnerMock{}
		runner.RunReturns(
			&claudelib.ClaudeResult{
				Result: "Based on the collected evidence, I can now make a determination. Let me synthesize:",
			},
			nil,
		)
		gh := &mocks.GhCli{}
		gh.FetchFailedLogsReturns("failing workflow log evidence", nil)
		step := buildStep(runner, gh)

		plan, result := updatepkg.RunDiagnosisForTest(
			step,
			&agentlib.Markdown{},
			"bborbe/http",
			"c052eef",
		)
		Expect(plan).To(BeNil())
		Expect(result).ToNot(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(result.Message).To(ContainSubstring("no usable fix-plan verdict"))
	})

	It("escalates to needs_input on a fabricated (unknown) verdict", func() {
		runner := &mocks.ClaudeRunnerMock{}
		runner.RunReturns(
			&claudelib.ClaudeResult{Result: `{"verdict":"bogus","reason":"made up"}`},
			nil,
		)
		gh := &mocks.GhCli{}
		gh.FetchFailedLogsReturns("failing workflow log evidence", nil)
		step := buildStep(runner, gh)

		plan, result := updatepkg.RunDiagnosisForTest(
			step,
			&agentlib.Markdown{},
			"bborbe/http",
			"c052eef",
		)
		Expect(plan).To(BeNil())
		Expect(result).ToNot(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(result.Message).To(ContainSubstring("no usable fix-plan verdict"))
	})
})
