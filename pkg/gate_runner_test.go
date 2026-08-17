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

// bigOutputLine is the 60-byte line a fixture gate target repeats to exceed
// gateTailMaxBytes (2000) of combined output.
const bigOutputLine = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

var _ = Describe("GateRunner", func() {
	var (
		ctx     context.Context
		workdir string
		runner  pkg.GateRunner
	)

	BeforeEach(func() {
		ctx = context.Background()
		runner = pkg.NewOSExecGateRunner()
		var err error
		workdir, err = os.MkdirTemp("", "gate-runner-test-")
		Expect(err).To(BeNil())
	})
	AfterEach(func() {
		Expect(os.RemoveAll(workdir)).To(Succeed())
	})

	Describe("RunTargetFull vs RunTarget tail bound", func() {
		BeforeEach(func() {
			makefile := "big:\n" +
				"\t@for i in $$(seq 1 60); do echo \"" + bigOutputLine + "\"; done\n"
			Expect(
				os.WriteFile(filepath.Join(workdir, "Makefile"), []byte(makefile), 0o644),
			).To(Succeed())
		})

		It("RunTargetFull returns the full output with no truncation marker", func() {
			output, exitCode, err := runner.RunTargetFull(ctx, workdir, "big")
			Expect(err).To(BeNil())
			Expect(exitCode).To(Equal(0))
			Expect(len(output)).To(BeNumerically(">", 2000))
			Expect(output).NotTo(ContainSubstring("...[truncated]"))
		})

		It("RunTarget returns the bounded tail with the truncation marker", func() {
			tail, exitCode, err := runner.RunTarget(ctx, workdir, "big")
			Expect(err).To(BeNil())
			Expect(exitCode).To(Equal(0))
			Expect(tail).To(ContainSubstring("...[truncated]"))
			Expect(len(tail)).To(BeNumerically("<", 60*(len(bigOutputLine)+1)))
		})
	})

	Describe("target validation", func() {
		It("rejects an invalid target name before any make invocation", func() {
			_, exitCode, err := runner.RunTarget(ctx, workdir, "evil;rm -rf")
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("invalid gate target name"))
			Expect(exitCode).To(Equal(-1))

			_, exitCode, err = runner.RunTargetFull(ctx, workdir, "evil;rm -rf")
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(ContainSubstring("invalid gate target name"))
			Expect(exitCode).To(Equal(-1))
		})
	})
})
