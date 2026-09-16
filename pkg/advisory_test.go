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

var _ = Describe("parseAdvisoryBlock", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	// parse crosses the real frontmatter parse (agentlib.ParseMarkdown on
	// literal markdown) rather than hand-constructing a TaskFrontmatter map —
	// the yaml.v3 decode into map[string]interface{} is the boundary this
	// feature depends on.
	parse := func(frontmatter string) (*pkg.AdvisoryBlock, error) {
		md, err := agentlib.ParseMarkdown(ctx, "---\n"+frontmatter+"---\n\nbody\n")
		Expect(err).To(BeNil())
		return pkg.ParseAdvisoryBlock(ctx, md)
	}

	It("returns nil, nil when the task carries no advisory block", func() {
		block, err := parse("repo: bborbe/demo\nref: abc123\n")
		Expect(err).To(BeNil())
		Expect(block).To(BeNil())
	})

	It("validates a well-formed block and stores the trimmed values", func() {
		block, err := parse("advisory:\n" +
			"  id: CVE-2026-12345\n" +
			"  package: golang.org/x/text\n" +
			"  fixed_version: v0.39.0\n" +
			"  source: osv-feed\n")
		Expect(err).To(BeNil())
		Expect(block).NotTo(BeNil())
		Expect(block.ID).To(Equal("CVE-2026-12345"))
		Expect(block.Package).To(Equal("golang.org/x/text"))
		Expect(block.FixedVersion).To(Equal("v0.39.0"))
		Expect(block.Source).To(Equal("osv-feed"))
	})

	It("ignores an unrecognized extra key", func() {
		block, err := parse("advisory:\n" +
			"  id: CVE-2026-12345\n" +
			"  package: golang.org/x/text\n" +
			"  fixed_version: v0.39.0\n" +
			"  source: osv-feed\n" +
			"  severity: HIGH\n")
		Expect(err).To(BeNil())
		Expect(block).NotTo(BeNil())
		Expect(block.ID).To(Equal("CVE-2026-12345"))
	})

	It("trims surrounding whitespace from every value", func() {
		block, err := parse("advisory:\n" +
			"  id: \"  CVE-2026-12345  \"\n" +
			"  package: \"  golang.org/x/text  \"\n" +
			"  fixed_version: \"  v0.39.0  \"\n" +
			"  source: \"  osv-feed  \"\n")
		Expect(err).To(BeNil())
		Expect(block).NotTo(BeNil())
		Expect(block.Package).To(Equal("golang.org/x/text"))
		Expect(block.FixedVersion).To(Equal("v0.39.0"))
		Expect(block.Source).To(Equal("osv-feed"))
	})

	DescribeTable("accepts every documented advisory-ID shape and rejects the rest",
		func(id string, accepted bool) {
			block, err := parse("advisory:\n" +
				"  id: \"" + id + "\"\n" +
				"  package: golang.org/x/text\n" +
				"  fixed_version: v0.39.0\n" +
				"  source: osv-feed\n")
			if accepted {
				Expect(err).To(BeNil())
				Expect(block).NotTo(BeNil())
				Expect(block.ID).To(Equal(id))
				return
			}
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("field=id"))
		},
		Entry("GO form", "GO-2026-1234", true),
		Entry("CVE form", "CVE-2026-9999", true),
		Entry("GHSA form", "GHSA-1234-5678-9012", true),
		Entry("ID with a trailing token", "GO-2026-1234extra", false),
		Entry("unknown prefix", "FOO-2026-1", false),
		Entry("two-digit year", "GO-26-1", false),
		Entry("empty", "", false),
	)

	It("rejects a list form, rendering the list in the message", func() {
		block, err := parse("advisory:\n" +
			"  - id: CVE-2026-12345\n" +
			"    package: golang.org/x/text\n" +
			"  - id: CVE-2026-54321\n" +
			"    package: golang.org/x/net\n")
		Expect(err).NotTo(BeNil())
		Expect(block).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("field=advisory"))
		Expect(err.Error()).To(ContainSubstring("CVE-2026-12345"))
	})

	It("rejects a scalar form", func() {
		block, err := parse("advisory: CVE-2026-12345\n")
		Expect(err).NotTo(BeNil())
		Expect(block).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("field=advisory"))
	})

	It("rejects a present-but-nil advisory key", func() {
		block, err := parse("advisory:\n")
		Expect(err).NotTo(BeNil())
		Expect(block).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("field=advisory"))
		Expect(err.Error()).To(ContainSubstring("value=<nil>"))
	})

	DescribeTable("rejects a missing or empty required key, naming the field",
		func(frontmatter, field string) {
			block, err := parse(frontmatter)
			Expect(err).NotTo(BeNil())
			Expect(block).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("field=" + field))
			Expect(err.Error()).To(ContainSubstring("value="))
		},
		Entry("package missing",
			"advisory:\n  id: CVE-2026-12345\n  fixed_version: v0.39.0\n  source: osv-feed\n",
			"package"),
		Entry(
			"package empty",
			"advisory:\n  id: CVE-2026-12345\n  package: \"\"\n  fixed_version: v0.39.0\n  source: osv-feed\n",
			"package",
		),
		Entry("fixed_version missing",
			"advisory:\n  id: CVE-2026-12345\n  package: golang.org/x/text\n  source: osv-feed\n",
			"fixed_version"),
		Entry(
			"fixed_version empty",
			"advisory:\n  id: CVE-2026-12345\n  package: golang.org/x/text\n  fixed_version: \"\"\n  source: osv-feed\n",
			"fixed_version",
		),
		Entry(
			"source missing",
			"advisory:\n  id: CVE-2026-12345\n  package: golang.org/x/text\n  fixed_version: v0.39.0\n",
			"source",
		),
		Entry(
			"source empty",
			"advisory:\n  id: CVE-2026-12345\n  package: golang.org/x/text\n  fixed_version: v0.39.0\n  source: \"\"\n",
			"source",
		),
	)

	DescribeTable("rejects an unparseable fixed_version, naming the raw value",
		func(raw string) {
			block, err := parse("advisory:\n" +
				"  id: CVE-2026-12345\n" +
				"  package: golang.org/x/text\n" +
				"  fixed_version: " + raw + "\n" +
				"  source: osv-feed\n")
			Expect(err).NotTo(BeNil())
			Expect(block).To(BeNil())
			Expect(err.Error()).To(ContainSubstring("field=fixed_version"))
			Expect(err.Error()).To(ContainSubstring("value=" + raw))
		},
		Entry("float", "1.2"),
		Entry("prose", "not-a-version"),
		Entry("version without the v prefix", "0.39.0"),
	)

	It("rejects a non-string fixed_version, rendering the raw YAML float", func() {
		block, err := parse("advisory:\n" +
			"  id: CVE-2026-12345\n" +
			"  package: golang.org/x/text\n" +
			"  fixed_version: 1.2\n" +
			"  source: osv-feed\n")
		Expect(err).NotTo(BeNil())
		Expect(block).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("field=fixed_version"))
		Expect(err.Error()).To(ContainSubstring("value=1.2"))
	})

	It("rejects a non-string id, rendering the raw value", func() {
		block, err := parse("advisory:\n" +
			"  id: 123\n" +
			"  package: golang.org/x/text\n" +
			"  fixed_version: v0.39.0\n" +
			"  source: osv-feed\n")
		Expect(err).NotTo(BeNil())
		Expect(block).To(BeNil())
		Expect(err.Error()).To(ContainSubstring("field=id"))
		Expect(err.Error()).To(ContainSubstring("value=123"))
	})

	It("keeps every rejection on the one-line message contract", func() {
		for _, frontmatter := range []string{
			"advisory: CVE-2026-12345\n",
			"advisory:\n  id: FOO-2026-1\n  package: p\n  fixed_version: v0.1.0\n  source: s\n",
			"advisory:\n  id: CVE-2026-1\n  package: \"\"\n  fixed_version: v0.1.0\n  source: s\n",
			"advisory:\n  id: CVE-2026-1\n  package: p\n  fixed_version: nope\n  source: s\n",
			"advisory:\n  id: CVE-2026-1\n  package: p\n  fixed_version: v0.1.0\n  source: \"\"\n",
		} {
			_, err := parse(frontmatter)
			Expect(err).NotTo(BeNil())
			Expect(
				err.Error(),
			).To(HavePrefix("invalid advisory frontmatter: field="))
			Expect(err.Error()).To(ContainSubstring("value="))
			Expect(err.Error()).NotTo(ContainSubstring("\n"))
		}
	})
})

var _ = Describe("advisoryFinding", func() {
	It("labels the row external:<source> and copies the other three fields", func() {
		row := pkg.AdvisoryFinding(&pkg.AdvisoryBlock{
			ID:           "CVE-2026-12345",
			Package:      "golang.org/x/text",
			FixedVersion: "v0.39.0",
			Source:       "osv-feed",
		})
		Expect(row.ID).To(Equal("CVE-2026-12345"))
		Expect(row.Package).To(Equal("golang.org/x/text"))
		Expect(row.FixedVersion).To(Equal("v0.39.0"))
		Expect(row.Scanner).To(Equal("external:osv-feed"))
	})
})
