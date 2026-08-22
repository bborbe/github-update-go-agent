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
			func(verdict string) {
				plan, err := updatepkg.ParseFixPlanForTest(
					ctx,
					`{"verdict":"`+verdict+`","reason":"r","episode_sha":"abc1234"}`,
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
