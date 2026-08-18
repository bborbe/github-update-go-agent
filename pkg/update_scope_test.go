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

var _ = Describe("UpdateScope", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Validate", func() {
		It("returns nil for every declared AvailableUpdateScope", func() {
			for _, scope := range pkg.AvailableUpdateScopes {
				err := scope.Validate(ctx)
				Expect(err).To(Succeed())
			}
		})
	})

	Describe("ParseUpdateScope", func() {
		It("returns UpdateScopeBoth with no error for empty string", func() {
			scope, err := pkg.ParseUpdateScope(ctx, "")
			Expect(scope).To(Equal(pkg.UpdateScopeBoth))
			Expect(err).To(Succeed())
		})

		It("returns UpdateScopeBoth for 'both'", func() {
			scope, err := pkg.ParseUpdateScope(ctx, "both")
			Expect(scope).To(Equal(pkg.UpdateScopeBoth))
			Expect(err).To(Succeed())
		})

		It("returns UpdateScopeGolang for 'golang'", func() {
			scope, err := pkg.ParseUpdateScope(ctx, "golang")
			Expect(scope).To(Equal(pkg.UpdateScopeGolang))
			Expect(err).To(Succeed())
		})

		It("returns UpdateScopeDeps for 'deps'", func() {
			scope, err := pkg.ParseUpdateScope(ctx, "deps")
			Expect(scope).To(Equal(pkg.UpdateScopeDeps))
			Expect(err).To(Succeed())
		})

		It("rejects 'bogus' with an error containing 'bogus', 'both', 'golang' and 'deps'", func() {
			_, err := pkg.ParseUpdateScope(ctx, "bogus")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring("bogus"))
			Expect(errMsg).To(ContainSubstring("both"))
			Expect(errMsg).To(ContainSubstring("golang"))
			Expect(errMsg).To(ContainSubstring("deps"))
		})

		It("rejects 'Both' (wrong case)", func() {
			_, err := pkg.ParseUpdateScope(ctx, "Both")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring("Both"))
			Expect(errMsg).To(ContainSubstring("both"))
			Expect(errMsg).To(ContainSubstring("golang"))
			Expect(errMsg).To(ContainSubstring("deps"))
		})

		It("rejects 'GOLANG' (wrong case)", func() {
			_, err := pkg.ParseUpdateScope(ctx, "GOLANG")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring("GOLANG"))
			Expect(errMsg).To(ContainSubstring("both"))
			Expect(errMsg).To(ContainSubstring("golang"))
			Expect(errMsg).To(ContainSubstring("deps"))
		})

		It("rejects ' both ' (spaces preserved)", func() {
			_, err := pkg.ParseUpdateScope(ctx, " both ")
			Expect(err).To(HaveOccurred())
			errMsg := err.Error()
			Expect(errMsg).To(ContainSubstring(" both "))
		})
	})

	Describe("IsBoth / IsGolangOnly / IsDepsOnly", func() {
		It("IsBoth returns true for UpdateScopeBoth and false for the others", func() {
			Expect(pkg.UpdateScopeBoth.IsBoth()).To(BeTrue())
			Expect(pkg.UpdateScopeGolang.IsBoth()).To(BeFalse())
			Expect(pkg.UpdateScopeDeps.IsBoth()).To(BeFalse())
		})

		It("IsGolangOnly returns true for UpdateScopeGolang and false for the others", func() {
			Expect(pkg.UpdateScopeGolang.IsGolangOnly()).To(BeTrue())
			Expect(pkg.UpdateScopeBoth.IsGolangOnly()).To(BeFalse())
			Expect(pkg.UpdateScopeDeps.IsGolangOnly()).To(BeFalse())
		})

		It("IsDepsOnly returns true for UpdateScopeDeps and false for the others", func() {
			Expect(pkg.UpdateScopeDeps.IsDepsOnly()).To(BeTrue())
			Expect(pkg.UpdateScopeBoth.IsDepsOnly()).To(BeFalse())
			Expect(pkg.UpdateScopeGolang.IsDepsOnly()).To(BeFalse())
		})
	})

	Describe("String", func() {
		It("returns 'golang' for UpdateScopeGolang", func() {
			Expect(pkg.UpdateScopeGolang.String()).To(Equal("golang"))
		})
	})
})
