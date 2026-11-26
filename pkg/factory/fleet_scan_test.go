// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/factory"
)

var _ = Describe("FleetScanFactory", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("composes exactly one execution phase with one LLM-free step", func() {
		phases := factory.FleetScanPhases(updatepkg.FleetScanDeps{})
		Expect(phases).To(HaveLen(1))
		Expect(phases[0].Name).To(Equal(domain.TaskPhaseExecution))
		Expect(phases[0].Steps).To(HaveLen(1))
		Expect(phases[0].Steps[0].Name()).To(Equal("fleet-scan"))
	})

	It("resolves the github-fleet-scan task type", func() {
		provider := factory.CreateAgentProvider(
			claudelib.ClaudeConfigDir(""),
			claudelib.AgentDir("agent"),
			claudelib.ClaudeModel("sonnet"),
			"gh-token",
			map[string]string{},
			factory.CreateGitOps(),
			factory.CreateGhCli("gh-token"),
			factory.CreateGateRunner(),
			factory.CreateClaudeProber(""),
			updatepkg.PRTargetDraft,
			"",
			updatepkg.UpdateScopeBoth,
			nil,
			"",
			updatepkg.FleetScanDeps{},
		)
		agent, err := provider.Get(ctx, agentlib.TaskType("github-fleet-scan"))
		Expect(err).To(BeNil())
		Expect(agent).NotTo(BeNil())
	})
})
