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

	Describe("hasWorkForScope vs the model's outcome label", func() {
		// Regression: bborbe/argument, 2026-08-19, update_scope=deps.
		// The model returned outcome=no_update_needed + has_work=false while
		// the SAME object carried dep_updates_expected=true, and its reason had
		// the scope inverted ("dep updates are out of scope since update_scope
		// is deps"). Planning closed on the label, so the task completed with no
		// PR and never retried — while the repo really had three direct dep
		// updates waiting (bborbe/errors, bborbe/time, onsi/ginkgo).
		It("reports in-scope work for the argument plan despite has_work=false", func() {
			plan := &pkg.PlanOutput{
				Outcome:            pkg.PlanOutcomeNoUpdateNeeded,
				HasWork:            false,
				DepUpdatesExpected: true,
				Reason:             "dep updates are out of scope since update_scope is deps",
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeDeps)
			Expect(pkg.HasWorkForScope(plan, pkg.UpdateScopeDeps)).To(BeTrue())
		})

		It("does NOT close the argument plan — the label loses to the fields", func() {
			// The regression guard. Under the old logic this returned true
			// (`Outcome == no_update_needed || !hasWork`) and the task completed
			// with no PR. Asserting on hasWorkForScope alone would NOT catch it.
			plan := &pkg.PlanOutput{
				Outcome:            pkg.PlanOutcomeNoUpdateNeeded,
				HasWork:            false,
				DepUpdatesExpected: true,
				Reason:             "dep updates are out of scope since update_scope is deps",
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeDeps)
			Expect(pkg.ShouldClose(plan, pkg.UpdateScopeDeps, "bborbe/argument")).To(BeFalse())
		})

		It("closes when the plan's own fields agree there is no work", func() {
			plan := &pkg.PlanOutput{
				Outcome:            pkg.PlanOutcomeNoUpdateNeeded,
				DepUpdatesExpected: false,
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeDeps)
			Expect(pkg.ShouldClose(plan, pkg.UpdateScopeDeps, "bborbe/example")).To(BeTrue())
		})

		It("closes a golang-scope plan whose only work is out-of-scope deps", func() {
			plan := &pkg.PlanOutput{
				DepUpdatesExpected: true,
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeGolang)
			Expect(pkg.ShouldClose(plan, pkg.UpdateScopeGolang, "bborbe/example")).To(BeTrue())
		})

		It("still reports no work when the deps scope genuinely has none", func() {
			plan := &pkg.PlanOutput{
				Outcome:            pkg.PlanOutcomeNoUpdateNeeded,
				DepUpdatesExpected: false,
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeDeps)
			Expect(pkg.HasWorkForScope(plan, pkg.UpdateScopeDeps)).To(BeFalse())
		})

		It("ignores a stale go directive on a deps-scope plan", func() {
			plan := &pkg.PlanOutput{
				GoBump:             &pkg.GoBump{From: "1.26.5", To: "1.26.6"},
				DepUpdatesExpected: false,
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeDeps)
			Expect(pkg.HasWorkForScope(plan, pkg.UpdateScopeDeps)).To(BeFalse())
		})

		It("ignores stale deps on a golang-scope plan", func() {
			plan := &pkg.PlanOutput{
				Outcome:            pkg.PlanOutcomeNoUpdateNeeded,
				DepUpdatesExpected: true,
			}
			pkg.AppliesScope(plan, pkg.UpdateScopeGolang)
			Expect(pkg.HasWorkForScope(plan, pkg.UpdateScopeGolang)).To(BeFalse())
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
