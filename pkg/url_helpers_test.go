// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("normalizeCloneURLToHTTPS", func() {
	DescribeTable("normalizes clone URL forms",
		func(in, expected string) {
			Expect(pkg.NormalizeCloneURLToHTTPS(in)).To(Equal(expected))
		},
		Entry(
			"scp form",
			"git@github.com:bborbe/demo.git",
			"https://github.com/bborbe/demo.git",
		),
		Entry(
			"ssh form",
			"ssh://git@github.com/bborbe/demo.git",
			"https://github.com/bborbe/demo.git",
		),
		Entry(
			"https unchanged",
			"https://github.com/bborbe/demo.git",
			"https://github.com/bborbe/demo.git",
		),
		Entry(
			"unrecognized unchanged",
			"ftp://example.com/x",
			"ftp://example.com/x",
		),
	)
})

var _ = Describe("injectToken", func() {
	It("injects x-access-token into HTTPS URLs", func() {
		Expect(pkg.InjectToken("https://github.com/bborbe/demo.git", "tok")).
			To(Equal("https://x-access-token:tok@github.com/bborbe/demo.git"))
	})

	It("returns the URL unchanged for empty token", func() {
		Expect(pkg.InjectToken("https://github.com/bborbe/demo.git", "")).
			To(Equal("https://github.com/bborbe/demo.git"))
	})

	It("returns non-HTTPS URLs unchanged", func() {
		Expect(pkg.InjectToken("git@github.com:bborbe/demo.git", "tok")).
			To(Equal("git@github.com:bborbe/demo.git"))
	})
})
