// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("typed output sections", func() {
	var ctx context.Context
	var md *agentlib.Markdown

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		md, err = agentlib.ParseMarkdown(ctx, "---\nrepo: bborbe/demo\n---\n\nbody\n")
		Expect(err).To(BeNil())
	})

	It("PlanOutput round-trips via MarshalSectionTyped + ExtractSection", func() {
		plan := pkg.PlanOutput{
			Outcome:            pkg.PlanOutcomeReady,
			HasWork:            true,
			GoBump:             &pkg.GoBump{From: "1.26.3", To: "1.26.5"},
			DepUpdatesExpected: true,
			GateTargets:        []string{"precommit", "check"},
			Vulns: []pkg.PlanVuln{
				{
					ID:           "GO-2026-1234",
					Package:      "golang.org/x/text",
					FixedVersion: "v0.39.0",
					Scanner:      "trivy",
					Action:       pkg.VulnActionFix,
					Reason:       "patched in v0.39.0",
				},
			},
		}
		section, err := agentlib.MarshalSectionTyped(ctx, "## Plan", plan)
		Expect(err).To(BeNil())
		md.ReplaceSection(section)

		got, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
		Expect(err).To(BeNil())
		Expect(*got).To(Equal(plan))
	})

	It("ResultOutput round-trips via MarshalSectionTyped + ExtractSection", func() {
		result := pkg.ResultOutput{
			Outcome:     pkg.ResultOutcomeOpened,
			Branch:      "fix/update-go-6d1f27f",
			PRURL:       "https://github.com/bborbe/demo/pull/42",
			GateExit:    0,
			DepsUpdated: 7,
			VulnsFixed:  []string{"GO-2026-1234"},
		}
		section, err := agentlib.MarshalSectionTyped(ctx, "## Result", result)
		Expect(err).To(BeNil())
		md.ReplaceSection(section)

		got, err := agentlib.ExtractSection[pkg.ResultOutput](ctx, md, "## Result")
		Expect(err).To(BeNil())
		Expect(*got).To(Equal(result))
	})

	It("ReviewOutput round-trips via MarshalSectionTyped + ExtractSection", func() {
		review := pkg.ReviewOutput{
			Approved: true,
			Checks: pkg.ReviewChecks{
				PROpen:              true,
				PRDraft:             true,
				GateGreen:           true,
				VulnsClear:          true,
				ChangelogUnreleased: true,
				NoNewTag:            true,
			},
			Notes: "all checks passed",
		}
		section, err := agentlib.MarshalSectionTyped(ctx, "## Review", review)
		Expect(err).To(BeNil())
		md.ReplaceSection(section)

		got, err := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
		Expect(err).To(BeNil())
		Expect(*got).To(Equal(review))
	})
})
