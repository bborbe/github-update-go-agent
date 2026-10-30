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

// stubStep returns a fixed Result, so the wrapper's own behaviour is what the
// spec observes.
type stubStep struct {
	result *agentlib.Result
}

func (s stubStep) Name() string { return "stub" }

func (s stubStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

func (s stubStep) Run(_ context.Context, _ *agentlib.Markdown) (*agentlib.Result, error) {
	return s.result, nil
}

var _ = Describe("humanReviewOwnerStep", func() {
	var ctx context.Context
	var md *agentlib.Markdown

	BeforeEach(func() {
		ctx = context.Background()
		md = &agentlib.Markdown{Frontmatter: agentlib.TaskFrontmatter{}}
	})

	It("stamps the owner and the reassignment declaration on a human_review result", func() {
		step := pkg.NewHumanReviewOwnerStep(
			stubStep{result: &agentlib.Result{NextPhase: "human_review"}},
			"bborbe",
		)
		_, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(md.Frontmatter).To(HaveKeyWithValue("assignee", "bborbe"))
		// Without this key the controller's operator-owned-assignee guard keeps
		// the on-disk agent assignee and discards the stamp — a silent no-op.
		Expect(md.Frontmatter).To(HaveKeyWithValue("assignee_reassigned_by", "stub"))
	})

	It("leaves a done result untouched — the record is finished, not parked", func() {
		step := pkg.NewHumanReviewOwnerStep(
			stubStep{result: &agentlib.Result{NextPhase: "done"}},
			"bborbe",
		)
		_, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(md.Frontmatter).NotTo(HaveKey("assignee"))
		Expect(md.Frontmatter).NotTo(HaveKey("assignee_reassigned_by"))
	})

	It("leaves a result with no NextPhase untouched", func() {
		step := pkg.NewHumanReviewOwnerStep(
			stubStep{result: &agentlib.Result{}},
			"bborbe",
		)
		_, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(md.Frontmatter).NotTo(HaveKey("assignee"))
	})

	It("is the identity when no owner is configured", func() {
		inner := stubStep{result: &agentlib.Result{NextPhase: "human_review"}}
		step := pkg.NewHumanReviewOwnerStep(inner, "")
		Expect(step).To(Equal(agentlib.Step(inner)))
		_, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(md.Frontmatter).NotTo(HaveKey("assignee"))
	})

	It("reports the owner it stamps through IsHumanReviewOwnerStep", func() {
		step := pkg.NewHumanReviewOwnerStep(
			stubStep{result: &agentlib.Result{NextPhase: "human_review"}},
			"bborbe",
		)
		owner, ok := pkg.IsHumanReviewOwnerStep(step)
		Expect(ok).To(BeTrue())
		Expect(owner).To(Equal("bborbe"))

		_, ok = pkg.IsHumanReviewOwnerStep(stubStep{})
		Expect(ok).To(BeFalse())
	})

	It("initialises a nil frontmatter map before stamping", func() {
		md.Frontmatter = nil
		step := pkg.NewHumanReviewOwnerStep(
			stubStep{result: &agentlib.Result{NextPhase: "human_review"}},
			"bborbe",
		)
		_, err := step.Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(md.Frontmatter).To(HaveKeyWithValue("assignee", "bborbe"))
	})
})
