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
// wrapper the factory applied to which phase body, not on what the body does,
// so the body itself is irrelevant.
type noopStep struct{ name string }

func (s noopStep) Name() string { return s.name }

func (s noopStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

func (s noopStep) Run(_ context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	return &agentlib.Result{}, nil
}

// wrapperIn returns the single wrapper in the named phase, or fails when the
// phase carries none or more than one.
//
// It asserts position and inner body, not just "this phase has a wrapper": a
// wrapper applied to claudeAuth instead of planningStep would satisfy a
// per-phase membership check while leaving the planning human_review route
// unstamped — the same silent-coverage failure these specs exist to prevent.
func wrapperIn(phases []agentlib.Phase, phase string) agentlib.Step {
	var found []agentlib.Step
	for _, step := range phaseNamed(phases, phase).Steps {
		if _, ok := updatepkg.IsHumanReviewOwnerStep(step); ok {
			found = append(found, step)
		}
	}
	ExpectWithOffset(
		1,
		found,
	).To(HaveLen(1), "phase %q must carry exactly one owner wrapper", phase)
	return found[0]
}

// phaseNamed returns the named phase, failing when it is absent. Every spec goes
// through it, so removing a phase from the factory fails loudly instead of
// leaving a spec's loop body unreachable and the spec vacuously green.
func phaseNamed(phases []agentlib.Phase, name string) agentlib.Phase {
	for _, p := range phases {
		if string(p.Name) == name {
			return p
		}
	}
	Fail("phase " + name + " is missing from the composed phase list")
	return agentlib.Phase{}
}

// These specs exist because a direct NewHumanReviewOwnerStep test passes while
// the wrapper is never composed into the phase list — exactly how the sibling
// agent's #61 shipped a stamp that fired nowhere. They assert on the phase list
// the factory actually builds, through the export_test seam.
var _ = Describe("human-review owner composition", func() {
	const owner = "bborbe"

	updater := func(owner string) []agentlib.Phase {
		return factory.UpdaterPhases(
			noopStep{"auth"}, noopStep{"gh-token"},
			noopStep{"planning"}, noopStep{"execution"}, noopStep{"review"},
			owner,
		)
	}
	buildFix := func(owner string) []agentlib.Phase {
		return factory.BuildFixPhases(
			noopStep{"auth"}, noopStep{"fix-planning"},
			noopStep{"fix-execution"}, noopStep{"fix-review"},
			owner,
		)
	}

	It("wraps the updater's planning body, not a neighbour in that phase", func() {
		step := wrapperIn(updater(owner), "planning")
		// The wrapper delegates Name to its inner step, so this is what proves it
		// wraps planningStep and not claudeAuth or ghTokenCheck.
		Expect(step.Name()).To(Equal("planning"))
		stamped, isWrapper := updatepkg.IsHumanReviewOwnerStep(step)
		Expect(isWrapper).To(BeTrue())
		Expect(stamped).To(Equal(owner))
	})

	It("wraps the updater's ai_review body", func() {
		step := wrapperIn(updater(owner), "ai_review")
		Expect(step.Name()).To(Equal("review"))
	})

	It("does not wrap the updater's execution phase — it never routes to human_review", func() {
		// Assert the phase exists first: a loop over matching phases passes
		// vacuously if the phase is removed entirely, which is a silent coverage
		// hole of exactly the kind this file exists to close.
		phase := phaseNamed(updater(owner), "execution")
		Expect(phase.Steps).NotTo(BeEmpty())
		for _, step := range phase.Steps {
			_, isWrapper := updatepkg.IsHumanReviewOwnerStep(step)
			Expect(isWrapper).To(BeFalse(), "execution must stay unwrapped")
		}
	})

	It("wraps the build-fixer's ai_review body — its only human_review site", func() {
		step := wrapperIn(buildFix(owner), "ai_review")
		Expect(step.Name()).To(Equal("fix-review"))
	})

	It("leaves the build-fixer's planning and execution phases unwrapped", func() {
		for _, name := range []string{"planning", "execution"} {
			phase := phaseNamed(buildFix(owner), name)
			Expect(phase.Steps).NotTo(BeEmpty(), "phase %q must exist to be checked", name)
			for _, step := range phase.Steps {
				_, isWrapper := updatepkg.IsHumanReviewOwnerStep(step)
				Expect(isWrapper).To(BeFalse(), "phase %q must stay unwrapped", name)
			}
		}
	})

	It("composes no wrapper when the owner is unconfigured, on either composition site", func() {
		for _, p := range append(updater(""), buildFix("")...) {
			for _, step := range p.Steps {
				_, isWrapper := updatepkg.IsHumanReviewOwnerStep(step)
				Expect(isWrapper).To(BeFalse(), "an unset owner must compose no wrapper")
			}
		}
	})
})
