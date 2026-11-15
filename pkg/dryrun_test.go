// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("DryRunResolve", func() {
	var (
		ctx context.Context
		ops *mocks.GitOps
	)

	BeforeEach(func() {
		ctx = context.Background()
		ops = &mocks.GitOps{}
		ops.ResolveDefaultBranchReturns(
			"main", "0cafebabe1234567890abcdef1234567890abcdef", nil,
		)
	})

	It("returns the resolved default branch and head SHA", func() {
		branch, head, err := pkg.DryRunResolve(ctx, ops, "tok", "bborbe/build-fix-canary-a")
		Expect(err).To(BeNil())
		Expect(branch).To(Equal("main"))
		Expect(head).To(Equal("0cafebabe1234567890abcdef1234567890abcdef"))
	})

	It("resolves against the token-authenticated HTTPS clone URL", func() {
		_, _, err := pkg.DryRunResolve(ctx, ops, "tok", "bborbe/build-fix-canary-a")
		Expect(err).To(BeNil())
		Expect(ops.ResolveDefaultBranchCallCount()).To(Equal(1))
		_, url := ops.ResolveDefaultBranchArgsForCall(0)
		Expect(url).To(Equal("https://x-access-token:tok@github.com/bborbe/build-fix-canary-a.git"))
	})

	It("writes nothing: no clone, no branch, no commit, no push", func() {
		_, _, err := pkg.DryRunResolve(ctx, ops, "tok", "bborbe/build-fix-canary-a")
		Expect(err).To(BeNil())
		Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		Expect(ops.SwitchNewBranchCallCount()).To(Equal(0))
		Expect(ops.CommitCallCount()).To(Equal(0))
		Expect(ops.PushCallCount()).To(Equal(0))
	})

	It("rejects a malformed repo before any git call", func() {
		_, _, err := pkg.DryRunResolve(ctx, ops, "tok", "not-a-repo")
		Expect(err).To(HaveOccurred())
		Expect(ops.ResolveDefaultBranchCallCount()).To(Equal(0))
	})

	It("propagates a resolution failure", func() {
		ops.ResolveDefaultBranchReturns("", "", stderrors.New("boom"))
		_, _, err := pkg.DryRunResolve(ctx, ops, "tok", "bborbe/build-fix-canary-a")
		Expect(err).To(HaveOccurred())
	})
})
