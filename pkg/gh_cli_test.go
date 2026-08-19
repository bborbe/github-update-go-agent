// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("prCreateArgs", func() {
	It("draft target includes --draft flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft, "")
		Expect(args).To(ContainElement("--draft"))
	})

	It("ready target omits --draft flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetReady, "")
		Expect(args).NotTo(ContainElement("--draft"))
	})

	It("empty label adds no --label flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft, "")
		Expect(args).NotTo(ContainElement("--label"))
	})

	It("non-empty label adds --label with the value", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft, "auto-merge")
		Expect(args).To(ContainElement("--label"))
		Expect(args).To(ContainElement("auto-merge"))
	})

	It("both targets start with pr create and include base, head, title, body", func() {
		for _, target := range []pkg.PRTarget{pkg.PRTargetDraft, pkg.PRTargetReady} {
			args := pkg.PRCreateArgs("mybase", "myhead", "mytitle", "mybody", target, "")
			Expect(args[0]).To(Equal("pr"))
			Expect(args[1]).To(Equal("create"))
			Expect(args).To(ContainElement("--base"))
			Expect(args).To(ContainElement("mybase"))
			Expect(args).To(ContainElement("--head"))
			Expect(args).To(ContainElement("myhead"))
			Expect(args).To(ContainElement("--title"))
			Expect(args).To(ContainElement("mytitle"))
			Expect(args).To(ContainElement("--body"))
			Expect(args).To(ContainElement("mybody"))
		}
	})
})

var _ = Describe("isMissingLabelError", func() {
	// Regression: 2026-08-19. AUTO_MERGE_LABEL=auto-merge was configured
	// fleet-wide but no repo defined the label, so every deps-sweep run died
	// at PR creation and the drain stopped producing PRs entirely.
	It("matches gh's missing-label refusal", func() {
		Expect(pkg.IsMissingLabelError(
			"could not add label: 'auto-merge' not found",
		)).To(BeTrue())
	})

	It("does not match an unrelated gh failure", func() {
		Expect(pkg.IsMissingLabelError(
			"pull request create failed: GraphQL: No commits between master and feature/x",
		)).To(BeFalse())
	})

	It("does not match a not-found error that is not about a label", func() {
		Expect(pkg.IsMissingLabelError(
			"HTTP 404: Not Found (https://api.github.com/repos/bborbe/nope)",
		)).To(BeFalse())
	})

	It("does not match empty output", func() {
		Expect(pkg.IsMissingLabelError("")).To(BeFalse())
	})
})
