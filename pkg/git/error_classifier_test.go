// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	stderrors "errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

var _ = Describe("ClassifyError", func() {
	It("returns the empty sentinel for nil", func() {
		Expect(git.ClassifyError(nil)).To(Equal(git.ErrorCategory("")))
	})

	DescribeTable("maps stderr fragments onto the closed enum",
		func(msg string, expected git.ErrorCategory) {
			Expect(git.ClassifyError(stderrors.New(msg))).To(Equal(expected))
		},
		Entry("403", "git clone: returned error: 403", git.ErrorCategoryAuth),
		Entry("401", "git clone: returned error: 401", git.ErrorCategoryAuth),
		Entry("auth failed", "fatal: Authentication failed", git.ErrorCategoryAuth),
		Entry("no username", "could not read Username", git.ErrorCategoryAuth),
		Entry(
			"not found",
			"remote: Repository not found.",
			git.ErrorCategoryRepoNotFound,
		),
		Entry(
			"protected",
			"GH006: Protected branch update failed",
			git.ErrorCategoryProtectedBranchRejected,
		),
		Entry(
			"non-fast-forward",
			"! [rejected] non-fast-forward",
			git.ErrorCategoryPushNonFastForward,
		),
		Entry("unknown", "something exploded", git.ErrorCategoryUnknown),
	)
})

var _ = Describe("RedactToken", func() {
	It("strips embedded x-access-token credentials", func() {
		Expect(
			git.RedactToken("https://x-access-token:ghs_secret123@github.com/o/r.git"),
		).To(Equal("https://x-access-token:[REDACTED]@github.com/o/r.git"))
	})
})
