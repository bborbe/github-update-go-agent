// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg/factory"
)

// The owner is written verbatim into task frontmatter and consumed cross-repo as
// an assignee, so a malformed deploy value would corrupt the YAML the controller
// parses, and an over-long one would be stamped and then never match the
// controller's HUMAN_ASSIGNEES set — reproducing the unowned record this stamp
// exists to fix, with no error anywhere.
var _ = Describe("ResolveHumanReviewOwner", func() {
	env := func(v string) map[string]string {
		return map[string]string{factory.HumanReviewOwnerEnv: v}
	}

	It("returns a configured login", func() {
		Expect(factory.ResolveHumanReviewOwner(env("bborbe"))).To(Equal("bborbe"))
	})

	It("trims surrounding whitespace", func() {
		Expect(factory.ResolveHumanReviewOwner(env("  bborbe\n"))).To(Equal("bborbe"))
	})

	It("returns empty when unset, disabling the stamp", func() {
		Expect(factory.ResolveHumanReviewOwner(map[string]string{})).To(BeEmpty())
	})

	It("accepts interior hyphens", func() {
		Expect(factory.ResolveHumanReviewOwner(env("ben-borbe"))).To(Equal("ben-borbe"))
	})

	DescribeTable("rejects a value that is not a usable GitHub login",
		func(value string) {
			Expect(factory.ResolveHumanReviewOwner(env(value))).To(BeEmpty())
			Expect(factory.IsHumanReviewOwnerValid(value)).To(BeFalse())
		},
		// A bot login here would re-create the unowned-record defect the stamp fixes.
		Entry("a bot suffix", "ben-s-go-updater-dev[bot]"),
		Entry("a leading hyphen", "-bborbe"),
		Entry("a trailing hyphen", "bborbe-"),
		Entry("an underscore", "ben_borbe"),
		Entry("an interior space", "ben borbe"),
		Entry("a slash", "ben/borbe"),
		// Past GitHub's 39-character limit the value would be stamped and then
		// never match HUMAN_ASSIGNEES, so the record is silently unowned again.
		Entry("40 characters", strings.Repeat("a", 40)),
	)

	It("accepts a login at exactly GitHub's 39-character limit", func() {
		atLimit := strings.Repeat("a", 39)
		Expect(factory.ResolveHumanReviewOwner(env(atLimit))).To(Equal(atLimit))
	})
})
