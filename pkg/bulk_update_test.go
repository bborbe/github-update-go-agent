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

var _ = Describe("BulkUpdater", func() {
	var (
		ctx     context.Context
		updater pkg.BulkUpdater
		workdir string
	)

	BeforeEach(func() {
		ctx = context.Background()
		updater = pkg.NewBulkUpdater()
		var err error
		workdir, err = os.MkdirTemp("", "bulk-update-*")
		Expect(err).To(BeNil())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(workdir)).To(Succeed())
	})

	Context("on a module with no dependencies", func() {
		BeforeEach(func() {
			Expect(os.WriteFile(
				filepath.Join(workdir, "go.mod"),
				[]byte("module example.com/x\n\ngo 1.26.6\n"),
				0o600,
			)).To(Succeed())
			Expect(os.WriteFile(
				filepath.Join(workdir, "main.go"),
				[]byte("package main\n\nfunc main() {}\n"),
				0o600,
			)).To(Succeed())
		})

		It("reports Ran with the command output", func() {
			result, err := updater.Run(ctx, workdir)
			Expect(err).To(BeNil())
			Expect(result.Ran).To(BeTrue())
			Expect(result.FailDetail).To(BeEmpty())
			Expect(result.Output).To(ContainSubstring("go get -u ./..."))
		})
	})

	Context("when the directory is not a Go module", func() {
		// Fail-closed is the point: a tooling failure must come back as
		// Ran=false with a reason (so the prompt can tell the model the deps
		// are NOT updated), never as a bare error that aborts the pipeline and
		// never as a silent success.
		It("reports Ran=false with a reason instead of erroring", func() {
			result, err := updater.Run(ctx, workdir)
			Expect(err).To(BeNil())
			Expect(result.Ran).To(BeFalse())
			Expect(result.FailDetail).NotTo(BeEmpty())
			Expect(strings.ToLower(result.FailDetail)).To(ContainSubstring("go get"))
		})
	})

	Context("when the context is already cancelled", func() {
		It("does not hang", func() {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			result, err := updater.Run(cancelled, workdir)
			Expect(err).To(BeNil())
			Expect(result.Ran).To(BeFalse())
		})
	})
})
