// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ScanWorkflowGoVersionPins", func() {
	var (
		ctx     context.Context
		tmpDir  string
		workdir string
	)

	writeWorkflow := func(rel string, content string) {
		path := filepath.Join(workdir, rel)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(
			os.WriteFile(path, []byte(content), 0o644),
		).To(Succeed())
		// #nosec G306 -- test fixture
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		tmpDir, err = os.MkdirTemp("", "workflow-pin-*")
		Expect(err).NotTo(HaveOccurred())
		workdir = tmpDir
	})

	AfterEach(func() {
		_ = os.RemoveAll(tmpDir) // #nosec G104 -- best-effort temp dir cleanup
	})

	Context("no workflows directory", func() {
		It("returns empty result with no error", func() {
			res, err := ScanWorkflowGoVersionPins(ctx, workdir)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.HasPlainPin()).To(BeFalse())
			Expect(res.PlainPins).To(BeEmpty())
			Expect(res.MatrixPins).To(BeEmpty())
		})
	})

	Context("plain single-value go-version pin", func() {
		BeforeEach(func() {
			writeWorkflow(".github/workflows/ci.yml", `name: CI
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
          cache: true
`)
		})

		It("classifies it as a plain (hardcoded) pin", func() {
			res, err := ScanWorkflowGoVersionPins(ctx, workdir)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.HasPlainPin()).To(BeTrue())
			Expect(res.PlainPins).To(HaveLen(1))
			Expect(res.PlainPins[0].File).To(Equal(".github/workflows/ci.yml"))
			Expect(res.PlainPins[0].Value).To(Equal("1.26.5"))
			Expect(res.MatrixPins).To(BeEmpty())
		})
	})

	Context("matrix go-version pin (deliberate multi-version test)", func() {
		BeforeEach(func() {
			writeWorkflow(".github/workflows/test.yml", `name: Test
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: ['1.25.11', '1.26.5']
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go-version }}
`)
		})

		It("classifies it as a matrix pin, not a hardcode", func() {
			res, err := ScanWorkflowGoVersionPins(ctx, workdir)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.HasPlainPin()).To(BeFalse())
			Expect(res.MatrixPins).To(HaveLen(2))
			Expect(res.PlainPins).To(BeEmpty())
		})
	})

	Context("both plain and matrix pins in one repo", func() {
		BeforeEach(func() {
			writeWorkflow(".github/workflows/ci.yml", `name: CI
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
`)
			writeWorkflow(".github/workflows/matrix.yaml", `name: Matrix
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: ['1.25.11', '1.26.5']
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go-version }}
`)
		})

		It("separates plain from matrix pins, both file suffixes handled", func() {
			res, err := ScanWorkflowGoVersionPins(ctx, workdir)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.HasPlainPin()).To(BeTrue())
			Expect(res.PlainPins).To(HaveLen(1))
			Expect(res.PlainPins[0].File).To(Equal(".github/workflows/ci.yml"))
			Expect(res.MatrixPins).To(HaveLen(2))
		})
	})

	Context("malformed YAML", func() {
		BeforeEach(func() {
			writeWorkflow(".github/workflows/bad.yml", `name: Bad
on: [push
  jobs: broken
`)
		})

		It("returns an error", func() {
			_, err := ScanWorkflowGoVersionPins(ctx, workdir)
			Expect(err).To(HaveOccurred())
		})
	})

	Context("non-yml files in workflows dir are ignored", func() {
		BeforeEach(func() {
			writeWorkflow(".github/workflows/notes.txt", "go-version: '1.26.5'\n")
		})

		It("returns empty result", func() {
			res, err := ScanWorkflowGoVersionPins(ctx, workdir)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.HasPlainPin()).To(BeFalse())
		})
	})
})
