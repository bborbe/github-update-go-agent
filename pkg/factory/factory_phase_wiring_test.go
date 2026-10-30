// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/factory"
)

// noopStep is an inert agentlib.Step: the composition specs assert on which
// wrapper the factory applied to a phase body, not on what the body does, so the
// body itself is irrelevant.
type noopStep struct{ name string }

func (s noopStep) Name() string { return s.name }

func (s noopStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

func (s noopStep) Run(_ context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	return &agentlib.Result{}, nil
}

// These specs exist because a direct NewHumanReviewOwnerStep test passes while
// the wrapper is never composed into the phase list — exactly how the sibling
// agent's #61 shipped a stamp that fired nowhere. They assert on the phase list
// the factory actually builds, through the export_test seam.
var _ = Describe("human-review owner composition", func() {
	const owner = "bborbe"

	// stepOwners reports, per phase, the owner stamped on each of that phase's
	// steps (empty string where the step is not a wrapper).
	stepOwners := func(phases []agentlib.Phase) map[string][]string {
		owners := map[string][]string{}
		for _, phase := range phases {
			for _, step := range phase.Steps {
				if o, ok := updatepkg.IsHumanReviewOwnerStep(step); ok {
					owners[string(phase.Name)] = append(owners[string(phase.Name)], o)
				}
			}
		}
		return owners
	}

	DescribeTable("the updater wraps every phase that can route to human_review",
		func(phase string) {
			phases := factory.UpdaterPhases(
				noopStep{"auth"}, noopStep{"gh-token"},
				noopStep{"planning"}, noopStep{"execution"}, noopStep{"review"},
				owner,
			)
			Expect(stepOwners(phases)).To(HaveKeyWithValue(phase, []string{owner}))
		},
		Entry("planning (malformed .maintainer.yaml)", "planning"),
		Entry("ai_review (approved, PR not yet merged)", "ai_review"),
	)

	It("does not wrap the updater's execution phase — it never routes to human_review", func() {
		phases := factory.UpdaterPhases(
			noopStep{"auth"}, noopStep{"gh-token"},
			noopStep{"planning"}, noopStep{"execution"}, noopStep{"review"},
			owner,
		)
		Expect(stepOwners(phases)).NotTo(HaveKey("execution"))
	})

	It("wraps the build-fixer's ai_review phase — its only human_review site", func() {
		phases := factory.BuildFixPhases(
			noopStep{"auth"}, noopStep{"fix-planning"},
			noopStep{"fix-execution"}, noopStep{"fix-review"},
			owner,
		)
		Expect(stepOwners(phases)).To(HaveKeyWithValue("ai_review", []string{owner}))
		Expect(stepOwners(phases)).NotTo(HaveKey("planning"))
		Expect(stepOwners(phases)).NotTo(HaveKey("execution"))
	})

	It("composes no wrapper when the owner is unconfigured", func() {
		phases := factory.UpdaterPhases(
			noopStep{"auth"}, noopStep{"gh-token"},
			noopStep{"planning"}, noopStep{"execution"}, noopStep{"review"},
			"",
		)
		Expect(stepOwners(phases)).To(BeEmpty())
	})
})
