// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("PRTarget", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Validate", func() {
		It("returns nil for every declared AvailablePRTarget", func() {
			for _, target := range pkg.AvailablePRTargets {
				err := target.Validate(ctx)
				Expect(err).To(Succeed())
			}
		})
	})

	Describe("ParsePRTarget", func() {
		It("returns PRTargetDraft with no error for empty string", func() {
			target, err := pkg.ParsePRTarget(ctx, "")
			Expect(target).To(Equal(pkg.PRTargetDraft))
			Expect(err).To(Succeed())
		})

		It("returns PRTargetDraft for 'draft'", func() {
			target, err := pkg.ParsePRTarget(ctx, "draft")
			Expect(target).To(Equal(pkg.PRTargetDraft))
			Expect(err).To(Succeed())
		})

		It("returns PRTargetReady for 'ready'", func() {
			target, err := pkg.ParsePRTarget(ctx, "ready")
			Expect(target).To(Equal(pkg.PRTargetReady))
			Expect(err).To(Succeed())
		})

		It("rejects 'bogus' with an error containing 'bogus', 'draft' and 'ready'", func() {
			_, err := pkg.ParsePRTarget(ctx, "bogus")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring("bogus"))
			Expect(errMsg).To(ContainSubstring("draft"))
			Expect(errMsg).To(ContainSubstring("ready"))
		})

		It("rejects 'Ready' (wrong case)", func() {
			_, err := pkg.ParsePRTarget(ctx, "Ready")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring("Ready"))
			Expect(errMsg).To(ContainSubstring("draft"))
			Expect(errMsg).To(ContainSubstring("ready"))
		})

		It("rejects 'DRAFT' (wrong case)", func() {
			_, err := pkg.ParsePRTarget(ctx, "DRAFT")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring("DRAFT"))
			Expect(errMsg).To(ContainSubstring("draft"))
			Expect(errMsg).To(ContainSubstring("ready"))
		})

		It("rejects ' draft ' (spaces preserved)", func() {
			_, err := pkg.ParsePRTarget(ctx, " draft ")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring(" draft "))
		})
	})

	Describe("IsDraft", func() {
		It("returns true for PRTargetDraft", func() {
			Expect(pkg.PRTargetDraft.IsDraft()).To(BeTrue())
		})

		It("returns false for PRTargetReady", func() {
			Expect(pkg.PRTargetReady.IsDraft()).To(BeFalse())
		})
	})

	Describe("String", func() {
		It("returns 'ready' for PRTargetReady", func() {
			Expect(pkg.PRTargetReady.String()).To(Equal("ready"))
		})
	})
})
