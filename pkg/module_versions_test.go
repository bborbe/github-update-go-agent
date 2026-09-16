// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("ModuleVersions", func() {
	Describe("parseModuleList", func() {
		// The canned string mirrors real `go list -m -f '{{.Path}} {{.Version}}'
		// all` output: the main module prints as a bare path with a trailing
		// space (its version column is empty), one blank line separates nothing
		// in particular, and go list emits no padding on dependency rows.
		It("parses the real command-output shape into rows in order", func() {
			output := "example.com/fixture \n" +
				"example.com/dep v1.2.0\n" +
				"\n" +
				"example.com/trailing   v0.3.0  \n"

			modules := pkg.ParseModuleList(output)

			Expect(modules).To(Equal([]pkg.ModuleVersion{
				{Path: "example.com/fixture"},
				{Path: "example.com/dep", Version: "v1.2.0"},
				{Path: "example.com/trailing", Version: "v0.3.0"},
			}))
		})

		It("returns nothing for empty output", func() {
			Expect(pkg.ParseModuleList("")).To(BeEmpty())
		})
	})

	Describe("moduleForPackage", func() {
		It("matches the module path itself", func() {
			modules := []pkg.ModuleVersion{{Path: "example.com/a", Version: "v1.0.0"}}

			module, found := pkg.ModuleForPackage(modules, "example.com/a")

			Expect(found).To(BeTrue())
			Expect(module).To(Equal(pkg.ModuleVersion{Path: "example.com/a", Version: "v1.0.0"}))
		})

		It("matches a package inside the module", func() {
			modules := []pkg.ModuleVersion{{Path: "example.com/a", Version: "v1.0.0"}}

			module, found := pkg.ModuleForPackage(modules, "example.com/a/sub")

			Expect(found).To(BeTrue())
			Expect(module.Path).To(Equal("example.com/a"))
		})

		It("prefers the longest matching module prefix", func() {
			modules := []pkg.ModuleVersion{
				{Path: "example.com/a", Version: "v1.0.0"},
				{Path: "example.com/a/b", Version: "v2.0.0"},
			}

			module, found := pkg.ModuleForPackage(modules, "example.com/a/b/c")

			Expect(found).To(BeTrue())
			Expect(module).To(Equal(pkg.ModuleVersion{Path: "example.com/a/b", Version: "v2.0.0"}))
		})

		It("does not match across a path-segment boundary", func() {
			modules := []pkg.ModuleVersion{{Path: "example.com/a", Version: "v1.0.0"}}

			_, found := pkg.ModuleForPackage(modules, "example.com/ab/c")

			Expect(found).To(BeFalse())
		})

		It("reports not-found when no module provides the package", func() {
			modules := []pkg.ModuleVersion{{Path: "example.com/a", Version: "v1.0.0"}}

			_, found := pkg.ModuleForPackage(modules, "example.com/other")

			Expect(found).To(BeFalse())
		})

		It("skips an empty module path", func() {
			modules := []pkg.ModuleVersion{
				{Path: "", Version: "v9.9.9"},
				{Path: "example.com/a", Version: "v1.0.0"},
			}

			module, found := pkg.ModuleForPackage(modules, "example.com/a")

			Expect(found).To(BeTrue())
			Expect(module.Path).To(Equal("example.com/a"))
		})
	})

	Describe("NewOSExecModuleResolver", func() {
		It("returns a non-nil resolver", func() {
			Expect(pkg.NewOSExecModuleResolver()).NotTo(BeNil())
		})

		It("lists the fixture module's own graph", func() {
			workdir, err := os.MkdirTemp("", "module-resolver-*")
			Expect(err).To(BeNil())
			defer func() { Expect(os.RemoveAll(workdir)).To(Succeed()) }()

			// Dependency-free fixture: no requires, so the listing needs no
			// network access.
			Expect(os.WriteFile(
				filepath.Join(workdir, "go.mod"),
				[]byte("module example.com/fixture\n\ngo 1.26.6\n"),
				0o600,
			)).To(Succeed())
			Expect(os.WriteFile(
				filepath.Join(workdir, "main.go"),
				[]byte("package main\n\nfunc main() {}\n"),
				0o600,
			)).To(Succeed())

			modules, err := pkg.NewOSExecModuleResolver().Modules(context.Background(), workdir)

			Expect(err).To(BeNil())
			Expect(modules).To(HaveLen(1))
			Expect(modules[0].Path).To(Equal("example.com/fixture"))
			Expect(modules[0].Version).To(BeEmpty())
		})
	})
})
