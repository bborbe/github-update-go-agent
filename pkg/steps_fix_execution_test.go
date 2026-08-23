// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"errors"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/git"
)

var _ = Describe("build-fix execution spec-filing dedup", func() {
	ctx := context.Background()

	buildStep := func(ops git.GitOps) agentlib.Step {
		return updatepkg.NewFixExecutionStep(ops, nil, "", nil)
	}

	Describe("branchExistsOnOrigin", func() {
		It("returns true when the branch clone succeeds (already filed)", func() {
			ops := &mocks.GitOps{}
			ops.CloneAtRefReturns(nil)
			step := buildStep(ops)
			Expect(
				updatepkg.BranchExistsOnOriginForTest(
					ctx,
					step,
					"bborbe/go-skeleton",
					"build-fixer/abc1234",
				),
			).To(BeTrue())
		})

		It(
			"returns false when the clone reports the branch missing (Remote branch ... not found)",
			func() {
				ops := &mocks.GitOps{}
				ops.CloneAtRefReturns(
					errors.New(
						"fatal: Remote branch build-fixer/abc1234 not found in upstream origin",
					),
				)
				step := buildStep(ops)
				Expect(
					updatepkg.BranchExistsOnOriginForTest(
						ctx,
						step,
						"bborbe/go-skeleton",
						"build-fixer/abc1234",
					),
				).To(BeFalse())
			},
		)

		It(
			"returns false when the checkout reports the branch missing (pathspec did not match any file(s))",
			func() {
				// Regression: CloneAtRef is a full clone + checkout, so a missing
				// branch surfaces as a checkout-stage pathspec error, not the
				// clone-stage "Remote branch ... not found". This must NOT be read
				// as "branch exists" — that false positive skipped the spec push.
				ops := &mocks.GitOps{}
				ops.CloneAtRefReturns(
					errors.New(
						"error: pathspec 'build-fixer/abc1234' did not match any file(s) known to git",
					),
				)
				step := buildStep(ops)
				Expect(
					updatepkg.BranchExistsOnOriginForTest(
						ctx,
						step,
						"bborbe/go-skeleton",
						"build-fixer/abc1234",
					),
				).To(BeFalse())
			},
		)

		It("returns false on 'not found' variants", func() {
			ops := &mocks.GitOps{}
			ops.CloneAtRefReturns(errors.New("Repository not found"))
			step := buildStep(ops)
			Expect(
				updatepkg.BranchExistsOnOriginForTest(
					ctx,
					step,
					"bborbe/go-skeleton",
					"build-fixer/abc1234",
				),
			).To(BeFalse())
		})
	})
})
