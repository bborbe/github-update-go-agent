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
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft)
		Expect(args).To(ContainElement("--draft"))
	})

	It("ready target omits --draft flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetReady)
		Expect(args).NotTo(ContainElement("--draft"))
	})

	It("both targets start with pr create and include base, head, title, body", func() {
		for _, target := range []pkg.PRTarget{pkg.PRTargetDraft, pkg.PRTargetReady} {
			args := pkg.PRCreateArgs("mybase", "myhead", "mytitle", "mybody", target)
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
