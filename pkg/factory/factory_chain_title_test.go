// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg/factory"
)

var _ = Describe("computeChainTitle", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("renders the frozen dash form, matching github-update-go-watcher", func() {
		Expect(
			factory.ComputeChainTitle("bborbe/http", "c052eef633bebc2d125ab8262e0284ee62ac0b32"),
		).
			To(Equal("Update Go bborbe-http c052eef"))
	})

	It("keeps a repo with no slash intact", func() {
		Expect(factory.ComputeChainTitle("http", "c052eef633bebc2d125ab8262e0284ee62ac0b32")).
			To(Equal("Update Go http c052eef"))
	})

	It("does not pad a SHA already at or below seven characters", func() {
		Expect(factory.ComputeChainTitle("bborbe/http", "c052eef")).
			To(Equal("Update Go bborbe-http c052eef"))
	})

	// The regression: the previous form was
	// "Update Go " + repo + " at " + shortSHA(...), which embeds the raw
	// owner/repo slash. task.CreateCommand.Validate rejects '/', so every
	// chain_update emit failed with "title contains forbidden character '/'"
	// and no updater task was ever created for a correctly diagnosed repo.
	// Asserting through Validate (not a string compare) is what makes this
	// test fail against the old code.
	DescribeTable(
		"emits a command whose title passes CreateCommand.Validate",
		func(repo string) {
			cmd := factory.BuildChainCommand(
				repo,
				"c052eef633bebc2d125ab8262e0284ee62ac0b32",
				[]string{"CI"},
			)
			Expect(cmd.Title).NotTo(ContainSubstring("/"))
			Expect(cmd.Validate(ctx)).To(Succeed())
		},
		Entry("owner/repo", "bborbe/http"),
		Entry("bare name", "http"),
		Entry("nested path", "bborbe/sub/http"),
	)
})
