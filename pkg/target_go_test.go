// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
)

func writeFixture(dir, rel, content string) {
	path := filepath.Join(dir, rel)
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(content), 0o644)).To(Succeed())
}

func releaseIndexServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

const setupGoWorkflow = `
jobs:
  test:
    steps:
      - uses: actions/checkout@v6
      - uses: actions/setup-go@v6
        with:
          go-version-file: go.mod
          cache: true
`

const releaseIndexBody = `[
  {"version":"go1.26.11","stable":true},
  {"version":"go1.27.3","stable":true},
  {"version":"go1.27.4","stable":true},
  {"version":"go1.27.5","stable":false},
  {"version":"go1.28.0","stable":true},
  {"version":"go1.28.3","stable":true}
]`

var _ = Describe("target go", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("GoMinorLine", func() {
		It("splits a patch version down to its minor line", func() {
			minor, ok := updatepkg.GoMinorLine("1.27.3")
			Expect(ok).To(BeTrue())
			Expect(minor).To(Equal("1.27"))
		})

		It("rejects a value without a minor component", func() {
			_, ok := updatepkg.GoMinorLine("1")
			Expect(ok).To(BeFalse())
		})
	})

	Describe("CIGoBaseline", func() {
		var workdir string

		BeforeEach(func() {
			workdir = GinkgoT().TempDir()
		})

		It("reads the go directive named by a setup-go go-version-file step", func() {
			writeFixture(workdir, ".github/workflows/ci.yml", setupGoWorkflow)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			baseline, ok := updatepkg.CIGoBaseline(workdir)
			Expect(ok).To(BeTrue())
			Expect(baseline).To(Equal("1.28.1"))
		})

		It("prefers the workflow that pins the repo-root go.mod over a sibling module", func() {
			writeFixture(workdir, ".github/workflows/a-docs.yml", `
jobs:
  docs:
    steps:
      - uses: actions/setup-go@v6
        with:
          go-version-file: hack/go.mod
`)
			writeFixture(workdir, "hack/go.mod", "module example.com/hack\n\ngo 1.26.0\n")
			writeFixture(workdir, ".github/workflows/z-ci.yml", setupGoWorkflow)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			baseline, ok := updatepkg.CIGoBaseline(workdir)
			Expect(ok).To(BeTrue())
			Expect(baseline).To(Equal("1.28.1"))
		})

		It("falls back to a pinned non-root module when no workflow pins the root go.mod", func() {
			writeFixture(workdir, ".github/workflows/ci.yml", `
jobs:
  docs:
    steps:
      - uses: actions/setup-go@v6
        with:
          go-version-file: hack/go.mod
`)
			writeFixture(workdir, "hack/go.mod", "module example.com/hack\n\ngo 1.26.0\n")

			baseline, ok := updatepkg.CIGoBaseline(workdir)
			Expect(ok).To(BeTrue())
			Expect(baseline).To(Equal("1.26.0"))
		})

		It("ignores a workflow whose setup-go step carries no go-version-file", func() {
			writeFixture(workdir, ".github/workflows/ci.yml", `
jobs:
  test:
    steps:
      - uses: actions/setup-go@v6
        with:
          go-version: "1.28.1"
`)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			_, ok := updatepkg.CIGoBaseline(workdir)
			Expect(ok).To(BeFalse())
		})

		It("ignores a workflow with no setup-go step", func() {
			writeFixture(workdir, ".github/workflows/ci.yml", `
jobs:
  test:
    steps:
      - uses: actions/checkout@v6
`)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			_, ok := updatepkg.CIGoBaseline(workdir)
			Expect(ok).To(BeFalse())
		})
	})

	Describe("GoDirective", func() {
		It("reads the version from the go directive", func() {
			workdir := GinkgoT().TempDir()
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n\nrequire (\n)\n")

			directive, ok := updatepkg.GoDirective(filepath.Join(workdir, "go.mod"))
			Expect(ok).To(BeTrue())
			Expect(directive).To(Equal("1.28.1"))
		})
	})

	Describe("LatestGoPatch", func() {
		It("picks the highest stable patch for the minor line", func() {
			server := releaseIndexServer(http.StatusOK, releaseIndexBody)
			defer server.Close()
			restore := updatepkg.SetGoReleaseIndexForTest(server.URL)
			defer restore()

			latest, err := updatepkg.LatestGoPatch(ctx, "1.27")
			Expect(err).To(BeNil())
			Expect(latest).To(Equal("1.27.4"))
		})

		It("reports an error when the minor line has no release", func() {
			server := releaseIndexServer(http.StatusOK, releaseIndexBody)
			defer server.Close()
			restore := updatepkg.SetGoReleaseIndexForTest(server.URL)
			defer restore()

			_, err := updatepkg.LatestGoPatch(ctx, "1.99")
			Expect(err).NotTo(BeNil())
		})

		It("reports an error on a non-200 status", func() {
			server := releaseIndexServer(http.StatusInternalServerError, "")
			defer server.Close()
			restore := updatepkg.SetGoReleaseIndexForTest(server.URL)
			defer restore()

			_, err := updatepkg.LatestGoPatch(ctx, "1.27")
			Expect(err).NotTo(BeNil())
		})
	})

	Describe("ResolveTargetGo", func() {
		// Every spec here pins a 1.28 minor line on purpose. The image runs a
		// 1.27 toolchain, so a target of 1.28.3 cannot have come from
		// targetGoVersion() — with a 1.27 fixture the fallback and the fix return
		// the same string whenever the image is current, and the spec would pass
		// against the bug it exists to catch.
		It("targets the index's latest patch, not the image toolchain", func() {
			server := releaseIndexServer(http.StatusOK, releaseIndexBody)
			defer server.Close()
			restore := updatepkg.SetGoReleaseIndexForTest(server.URL)
			defer restore()

			workdir := GinkgoT().TempDir()
			writeFixture(workdir, ".github/workflows/ci.yml", setupGoWorkflow)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			baseline, ok := updatepkg.CIGoBaseline(workdir)
			Expect(ok).To(BeTrue())
			Expect(baseline).To(Equal("1.28.1"))

			target := updatepkg.ResolveTargetGo(ctx, workdir)
			Expect(target).To(Equal("1.28.3"))
			Expect(target).NotTo(Equal(baseline))
			Expect(target).NotTo(Equal(updatepkg.TargetGoVersion()))
		})

		It("falls back to the image toolchain when no setup-go step exists", func() {
			workdir := GinkgoT().TempDir()
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			Expect(updatepkg.ResolveTargetGo(ctx, workdir)).To(Equal(updatepkg.TargetGoVersion()))
		})

		It("keeps the baseline when the release index is unavailable", func() {
			server := releaseIndexServer(http.StatusInternalServerError, "")
			defer server.Close()
			restore := updatepkg.SetGoReleaseIndexForTest(server.URL)
			defer restore()

			workdir := GinkgoT().TempDir()
			writeFixture(workdir, ".github/workflows/ci.yml", setupGoWorkflow)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			Expect(updatepkg.ResolveTargetGo(ctx, workdir)).To(Equal("1.28.1"))
		})

		It("never returns a target below the baseline", func() {
			// The index only knows 1.27, while the repo is already on 1.28.1.
			server := releaseIndexServer(http.StatusOK, `[
  {"version":"go1.27.4","stable":true}
]`)
			defer server.Close()
			restore := updatepkg.SetGoReleaseIndexForTest(server.URL)
			defer restore()

			workdir := GinkgoT().TempDir()
			writeFixture(workdir, ".github/workflows/ci.yml", setupGoWorkflow)
			writeFixture(workdir, "go.mod", "module example.com/x\n\ngo 1.28.1\n")

			Expect(updatepkg.ResolveTargetGo(ctx, workdir)).To(Equal("1.28.1"))
		})
	})
})
