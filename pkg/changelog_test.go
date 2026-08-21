// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("ensureChangelog", func() {
	var (
		ctx       context.Context
		workdir   string
		baseGoMod []byte
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		workdir, err = os.MkdirTemp("", "changelog-*")
		Expect(err).To(BeNil())
		baseGoMod = []byte(
			"module example.com/x\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/foo/bar v1.2.0\n\tgithub.com/baz/qux v0.3.1\n)\n",
		)
	})

	AfterEach(func() {
		Expect(os.RemoveAll(workdir)).To(Succeed())
	})

	writeGoMod := func(content string) {
		Expect(os.WriteFile(filepath.Join(workdir, "go.mod"), []byte(content), 0o600)).To(Succeed())
	}

	readChangelog := func() string {
		b, err := os.ReadFile(filepath.Join(workdir, "CHANGELOG.md"))
		Expect(err).To(BeNil())
		return string(b)
	}

	Context("when CHANGELOG.md does not exist", func() {
		BeforeEach(func() {
			// go.mod with a bumped bar version + a go-directive bump.
			writeGoMod(
				"module example.com/x\n\ngo 1.26.7\n\nrequire (\n\tgithub.com/foo/bar v1.3.0\n\tgithub.com/baz/qux v0.3.1\n)\n",
			)
		})

		It("creates the file with the canonical preamble before ## Unreleased", func() {
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(result.Created).To(BeTrue())
			content := readChangelog()
			Expect(content).To(HavePrefix("# Changelog\n"))
			// Preamble must precede ## Unreleased (rule changelog/preamble-frozen).
			Expect(
				strings.Index(content, "Please choose versions by [Semantic Versioning]"),
			).To(BeNumerically(">", 0))
			Expect(
				strings.Index(content, "## Unreleased"),
			).To(BeNumerically(">", strings.Index(content, "PATCH version")))
		})

		It("writes a chore: bullet naming the actually-bumped modules", func() {
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(
				result.Bullet,
			).To(Equal("- chore: update Go to 1.26.7 and github.com/foo/bar to v1.3.0"))
			content := readChangelog()
			Expect(
				content,
			).To(ContainSubstring("- chore: update Go to 1.26.7 and github.com/foo/bar to v1.3.0"))
		})

		It("leaves the unchanged module out of the bullet", func() {
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(result.Bullet).NotTo(ContainSubstring("github.com/baz/qux"))
		})
	})

	Context("when CHANGELOG.md exists with an empty ## Unreleased section", func() {
		BeforeEach(func() {
			writeGoMod(
				"module example.com/x\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/foo/bar v1.3.0\n\tgithub.com/baz/qux v0.3.1\n)\n",
			)
			Expect(os.WriteFile(
				filepath.Join(workdir, "CHANGELOG.md"),
				[]byte("# Changelog\n\n## Unreleased\n\n## v0.2.0\n\n- fix: something\n"),
				0o600,
			)).To(Succeed())
		})

		It("inserts the bullet inside the existing ## Unreleased section", func() {
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(result.Created).To(BeFalse())
			content := readChangelog()
			Expect(
				content,
			).To(ContainSubstring("## Unreleased\n\n- chore: update github.com/foo/bar to v1.3.0\n\n## v0.2.0"))
		})

		It("never inserts ## Unreleased above the preamble", func() {
			_, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			content := readChangelog()
			Expect(strings.Index(content, "# Changelog")).To(Equal(0))
			Expect(
				strings.Index(content, "## Unreleased"),
			).To(BeNumerically(">", strings.Index(content, "PATCH version")))
		})
	})

	Context("when CHANGELOG.md exists without any ## Unreleased section", func() {
		BeforeEach(func() {
			writeGoMod(
				"module example.com/x\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/foo/bar v1.3.0\n)\n",
			)
			Expect(os.WriteFile(
				filepath.Join(workdir, "CHANGELOG.md"),
				[]byte(
					"# Changelog\n\nPlease choose versions by [Semantic Versioning](http://semver.org/).\n\n## v0.1.0\n\n- fix: initial\n",
				),
				0o600,
			)).To(Succeed())
		})

		It("inserts ## Unreleased after the preamble, above the newest version", func() {
			_, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			content := readChangelog()
			Expect(
				content,
			).To(ContainSubstring("## Unreleased\n\n- chore: update github.com/foo/bar to v1.3.0\n\n## v0.1.0"))
			Expect(
				strings.Index(content, "## Unreleased"),
			).To(BeNumerically(">", strings.Index(content, "PATCH version")))
		})
	})

	Context("when the ## Unreleased section already has a conventional-prefix bullet", func() {
		BeforeEach(func() {
			writeGoMod(
				"module example.com/x\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/foo/bar v1.3.0\n)\n",
			)
			Expect(os.WriteFile(
				filepath.Join(workdir, "CHANGELOG.md"),
				[]byte(
					"# Changelog\n\n## Unreleased\n\n- chore: update github.com/foo/bar to v1.3.0\n\n## v0.1.0\n",
				),
				0o600,
			)).To(Succeed())
		})

		It("leaves the file untouched", func() {
			before := readChangelog()
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(result.Bullet).To(BeEmpty())
			Expect(readChangelog()).To(Equal(before))
		})
	})

	Context("when the ## Unreleased section has only an unprefixed bullet", func() {
		BeforeEach(func() {
			writeGoMod(
				"module example.com/x\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/foo/bar v1.3.0\n)\n",
			)
			Expect(os.WriteFile(
				filepath.Join(workdir, "CHANGELOG.md"),
				[]byte("# Changelog\n\n## Unreleased\n\n- update dependencies\n\n## v0.1.0\n"),
				0o600,
			)).To(Succeed())
		})

		It("appends a conventional-prefix bullet", func() {
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(result.Bullet).To(Equal("- chore: update github.com/foo/bar to v1.3.0"))
			Expect(
				readChangelog(),
			).To(ContainSubstring("- chore: update github.com/foo/bar to v1.3.0"))
		})
	})

	Context("when the update changed no go.mod requirement versions", func() {
		BeforeEach(func() {
			writeGoMod(
				"module example.com/x\n\ngo 1.26.6\n\nrequire (\n\tgithub.com/foo/bar v1.2.0\n)\n",
			)
		})

		It("falls back to the generic dependency bullet", func() {
			result, err := pkg.EnsureChangelog(ctx, workdir, baseGoMod)
			Expect(err).To(BeNil())
			Expect(result.Bullet).To(Equal("- chore: update go module dependencies"))
		})
	})
})
