// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("YourMoveBody", func() {
	It("renders the PR URL unavailable placeholder for an empty PRURL", func() {
		body := pkg.BuildYourMoveBody(&pkg.ResultOutput{}, &pkg.PlanOutput{})
		Expect(body).To(ContainSubstring("PR URL unavailable"))
		Expect(body).To(ContainSubstring("**Merge the PR** to apply the update."))
		Expect(body).To(ContainSubstring("No version bump recorded"))
		Expect(body).NotTo(ContainSubstring("["))
		Expect(body).NotTo(ContainSubstring("{"))
	})

	It("renders the PR URL unavailable placeholder for a non-http(s) PRURL", func() {
		body := pkg.BuildYourMoveBody(
			&pkg.ResultOutput{PRURL: "javascript:alert(1)"},
			&pkg.PlanOutput{},
		)
		Expect(body).To(ContainSubstring("PR URL unavailable"))
		Expect(body).NotTo(ContainSubstring("javascript"))
		Expect(body).NotTo(ContainSubstring("{"))
	})

	It("renders the PR URL unavailable placeholder for an unparseable PRURL", func() {
		body := pkg.BuildYourMoveBody(&pkg.ResultOutput{PRURL: "%zz"}, &pkg.PlanOutput{})
		Expect(body).To(ContainSubstring("PR URL unavailable"))
		Expect(body).NotTo(ContainSubstring("{"))
	})

	It("renders a clickable PR link for a valid http(s) PRURL", func() {
		body := pkg.BuildYourMoveBody(
			&pkg.ResultOutput{PRURL: "https://github.com/bborbe/demo/pull/7"},
			&pkg.PlanOutput{},
		)
		Expect(body).To(ContainSubstring("[Open the PR](https://github.com/bborbe/demo/pull/7)"))
		Expect(body).To(ContainSubstring("No version bump recorded"))
		Expect(body).NotTo(ContainSubstring("{"))
	})

	It("interpolates the Go version bump from and to", func() {
		body := pkg.BuildYourMoveBody(
			&pkg.ResultOutput{PRURL: "https://github.com/bborbe/demo/pull/7"},
			&pkg.PlanOutput{GoBump: &pkg.GoBump{From: "1.26.4", To: "1.26.6"}},
		)
		Expect(body).To(ContainSubstring("Go version bump: 1.26.4 → 1.26.6"))
		Expect(body).NotTo(ContainSubstring("No version bump recorded"))
		Expect(body).NotTo(ContainSubstring("{"))
	})

	It("interpolates the dependency count and fixed-vulnerability IDs", func() {
		body := pkg.BuildYourMoveBody(
			&pkg.ResultOutput{
				PRURL:       "https://github.com/bborbe/demo/pull/7",
				DepsUpdated: 3,
				VulnsFixed:  []string{"GO-2024-1234", "CVE-2025-1000"},
			},
			&pkg.PlanOutput{},
		)
		Expect(body).To(ContainSubstring("Updated 3 dependencies"))
		Expect(body).To(ContainSubstring("Fixed vulnerabilities: GO-2024-1234, CVE-2025-1000"))
		Expect(body).NotTo(ContainSubstring("No version bump recorded"))
		Expect(body).NotTo(ContainSubstring("{"))
	})
})
