// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("fix plan helpers", func() {
	ctx := context.Background()

	Describe("parseFixPlan", func() {
		DescribeTable("accepts each valid verdict",
			func(verdict updatepkg.FixVerdict) {
				plan, err := updatepkg.ParseFixPlanForTest(
					ctx,
					`{"verdict":"`+string(verdict)+`","reason":"r","episode_sha":"abc1234"}`,
				)
				Expect(err).To(BeNil())
				Expect(plan.Verdict).To(Equal(verdict))
				Expect(plan.EpisodeSHA).To(Equal("abc1234"))
			},
			Entry("no_fix_needed", updatepkg.FixVerdictNoFixNeeded),
			Entry("chain_update", updatepkg.FixVerdictChainUpdate),
			Entry("file_spec", updatepkg.FixVerdictFileSpec),
			Entry("needs_input", updatepkg.FixVerdictNeedsInput),
		)

		It("rejects an unknown verdict", func() {
			_, err := updatepkg.ParseFixPlanForTest(ctx, `{"verdict":"bogus"}`)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid build-fix verdict"))
		})

		It("rejects empty output", func() {
			_, err := updatepkg.ParseFixPlanForTest(ctx, "  ")
			Expect(err).To(HaveOccurred())
		})

		It("rejects malformed JSON", func() {
			_, err := updatepkg.ParseFixPlanForTest(ctx, `{"verdict":`)
			Expect(err).To(HaveOccurred())
		})

		// The regression: parseFixPlan used a bare json.Unmarshal, so a model
		// that answered in prose before the verdict JSON failed with
		// "invalid character 'B' looking for beginning of value" and every
		// such run landed in ## Failure (prod pod
		// build-fix-agent-63897e93-20260902194936, episode c052eef,
		// 2026-09-02). Routing through parseJSONResponse's extraction
		// strategies recovers the verdict from prose-wrapped output.
		DescribeTable(
			"recovers a JSON verdict wrapped in model prose",
			func(response string) {
				plan, err := updatepkg.ParseFixPlanForTest(ctx, response)
				Expect(err).To(BeNil())
				Expect(plan.Verdict).To(Equal(updatepkg.FixVerdictChainUpdate))
				Expect(plan.EpisodeSHA).To(Equal("c052eef633bebc2d125ab8262e0284ee62ac0b32"))
			},
			Entry(
				"prose before JSON",
				`Based on the collected evidence, I can now make a determination. Let me synthesize: {"verdict":"chain_update","reason":"golangci-lint v2.11.3 pinned in go.mod cannot parse Go 1.27 export data","failing_workflows":["CI"],"episode_sha":"c052eef633bebc2d125ab8262e0284ee62ac0b32"}`,
			),
			Entry(
				"prose before fenced JSON",
				`I'm unable to complete this task. {"verdict":"chain_update","reason":"r","episode_sha":"c052eef633bebc2d125ab8262e0284ee62ac0b32"}`,
			),
			Entry(
				"prose after JSON",
				`{"verdict":"chain_update","reason":"r","episode_sha":"c052eef633bebc2d125ab8262e0284ee62ac0b32"} Let me verify this conclusion.`,
			),
			Entry(
				"fenced block with leading prose",
				"Here is the diagnosis:\n```json\n{\"verdict\":\"chain_update\",\"reason\":\"r\",\"episode_sha\":\"c052eef633bebc2d125ab8262e0284ee62ac0b32\"}\n```\nEnd of synthesis.",
			),
		)

		It("still fails cleanly when no JSON verdict is recoverable", func() {
			_, err := updatepkg.ParseFixPlanForTest(
				ctx,
				"Based on the collected evidence, I can now make a determination. Let me synthesize:",
			)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unmarshal build-fix plan"))
			Expect(err.Error()).NotTo(ContainSubstring("invalid character"))
		})
	})

	Describe("shortSHA", func() {
		It("returns the 7-char prefix for long SHAs", func() {
			Expect(updatepkg.ShortSHAForTest("abcdef1234567890")).To(Equal("abcdef1"))
		})

		It("returns short strings unchanged", func() {
			Expect(updatepkg.ShortSHAForTest("abc")).To(Equal("abc"))
		})
	})

	Describe("sanitizeSlug", func() {
		It("lowercases and replaces separators", func() {
			// "&" is dropped (not in the safe set); each separator becomes "-"
			// (space→-, /→-, space→- around the dropped &).
			Expect(updatepkg.SanitizeSlugForTest("CI / Build & Test")).To(Equal("ci---build--test"))
		})

		It("falls back to workflow for empty slugs", func() {
			Expect(updatepkg.SanitizeSlugForTest("!!!")).To(Equal("workflow"))
		})
	})

	Describe("BuildBugSpec", func() {
		It("renders a dark-factory-valid kind:bug spec", func() {
			plan := &updatepkg.FixPlanOutput{
				Verdict:          updatepkg.FixVerdictFileSpec,
				Reason:           "go test ./... failed on TestFoo",
				FailingWorkflows: []string{"CI"},
				EpisodeSHA:       "abcdef1",
			}
			spec := updatepkg.BuildBugSpec("bborbe/demo", plan, "log evidence here")
			Expect(spec).To(ContainSubstring("status: idea"))
			Expect(spec).To(ContainSubstring("kind: bug"))
			Expect(spec).To(ContainSubstring("bborbe/demo"))
			Expect(spec).To(ContainSubstring("go test ./... failed on TestFoo"))
			Expect(spec).To(ContainSubstring("log evidence here"))
		})
	})
})
