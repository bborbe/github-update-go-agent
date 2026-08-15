// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"os"
	"path/filepath"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	libkafka "github.com/bborbe/kafka"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/factory"
)

var _ = Describe("CreateAgentProvider", func() {
	var (
		ctx      context.Context
		provider agentlib.AgentProvider
	)

	BeforeEach(func() {
		ctx = context.Background()
		provider = factory.CreateAgentProvider(
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
		)
	})

	It("returns a non-nil provider", func() {
		Expect(provider).NotTo(BeNil())
	})

	It("Get returns the domain agent for task_type github-update-go", func() {
		agent, err := provider.Get(ctx, agentlib.TaskType("github-update-go"))
		Expect(err).To(BeNil())
		Expect(agent).NotTo(BeNil())
	})

	It("Get returns the liveness agent for TaskTypeHealthcheck", func() {
		agent, err := provider.Get(ctx, agentlib.TaskTypeHealthcheck)
		Expect(err).To(BeNil())
		Expect(agent).NotTo(BeNil())
	})

	It("Get returns the SAME liveness agent for TaskTypeOAuthProbe (alias)", func() {
		healthcheckAgent, err := provider.Get(ctx, agentlib.TaskTypeHealthcheck)
		Expect(err).To(BeNil())
		oauthProbeAgent, err := provider.Get(ctx, agentlib.TaskTypeOAuthProbe)
		Expect(err).To(BeNil())
		Expect(oauthProbeAgent).To(BeIdenticalTo(healthcheckAgent))
	})

	Describe("Get with unknown task_type", func() {
		var err error

		BeforeEach(func() {
			_, err = provider.Get(ctx, agentlib.TaskType("bogus"))
		})

		It("returns an error", func() {
			Expect(err).To(HaveOccurred())
		})

		It("error message contains the unknown task_type literal", func() {
			Expect(err.Error()).To(ContainSubstring("unknown task_type"))
		})

		It("error message contains the offending value quoted", func() {
			Expect(err.Error()).To(ContainSubstring(`"bogus"`))
		})

		It("error message contains the binary name", func() {
			Expect(err.Error()).To(ContainSubstring("github-update-go-agent"))
		})

		It("error message contains the sorted accepted-types list", func() {
			Expect(err.Error()).To(ContainSubstring("[github-update-go healthcheck oauth-probe]"))
		})
	})
})

var _ = Describe("CreateSyncProducer", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns an error when broker is unreachable", func() {
		producer, err := factory.CreateSyncProducer(ctx, libkafka.Brokers{})
		Expect(producer).To(BeNil())
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("create sync producer"))
	})
})

var _ = Describe("CreateKafkaResultDeliverer", func() {
	It("returns a non-nil ResultDeliverer", func() {
		deliverer := factory.CreateKafkaResultDeliverer(
			nil,
			"",
			"",
			"",
			nil,
		)
		Expect(deliverer).NotTo(BeNil())
	})
})

var _ = Describe("CreateFileResultDeliverer", func() {
	It("returns a non-nil ResultDeliverer", func() {
		deliverer := factory.CreateFileResultDeliverer("/tmp/test-output.md")
		Expect(deliverer).NotTo(BeNil())
	})
})

var _ = Describe("CreateAgent", func() {
	It("returns a non-nil *agentlib.Agent", func() {
		agent := factory.CreateAgent(
			"",
			"",
			"",
			"",
			nil,
			factory.CreateGitOps(),
			factory.CreateGhCli(""),
			factory.CreateGateRunner(),
			factory.CreateClaudeProber(""),
			updatepkg.PRTargetDraft,
		)
		Expect(agent).NotTo(BeNil())
	})
})

const reviewTaskMD = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: ai_review
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-1
---

Update Go bborbe/demo

## Plan

` + "```json" + `
{
  "outcome": "ready",
  "has_work": true,
  "dep_updates_expected": true,
  "gate_targets": ["precommit"]
}
` + "```" + `

## Result

` + "```json" + `
{
  "outcome": "opened",
  "branch": "fix/update-go-6d1f27f",
  "pr_url": "https://github.com/bborbe/demo/pull/42",
  "gate_exit": 0
}
` + "```" + `
`

// changelogMaster is the master CHANGELOG; changelogBranch adds only an
// Unreleased bullet (the compliant shape).
const changelogMaster = "# Changelog\n\n## Unreleased\n\n## v1.2.3\n\n- old release\n"

const changelogBranch = "# Changelog\n\n## Unreleased\n\n- update Go to 1.26.5 and update dependencies\n\n## v1.2.3\n\n- old release\n"

var _ = Describe("CreateAgent with PRTargetReady", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("approves a non-draft PR through the production wiring", func() {
		var (
			agent *agentlib.Agent
			ops   *mocks.GitOps
			gh    *mocks.GhCli
			gate  *mocks.GateRunner
		)

		BeforeEach(func() {
			ops = &mocks.GitOps{}
			gh = &mocks.GhCli{}
			gate = &mocks.GateRunner{}

			// non-draft PR
			gh.ViewPRReturns("OPEN", false, nil)
			ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
				if err := os.MkdirAll(workdir, 0o750); err != nil {
					return err
				}
				return os.WriteFile(
					filepath.Join(workdir, "CHANGELOG.md"),
					[]byte(changelogBranch),
					0o600,
				)
			}
			ops.ShowFileReturns([]byte(changelogMaster), nil)
			gate.RunTargetReturns("", 0, nil)
			ops.RevListReturns([]string{"deadbeef1", "deadbeef2"}, nil)
			ops.LsRemoteTagsReturns([]string{"1111111", "2222222"}, nil)

			agent = factory.CreateAgent(
				"", "", "", "tok", nil,
				ops, gh, gate,
				factory.CreateClaudeProber(""),
				updatepkg.PRTargetReady,
			)
		})

		It("returns Done + human_review for a matching non-draft PR", func() {
			result, err := agent.Run(
				ctx,
				domain.TaskPhaseAIReview,
				reviewTaskMD,
				delivery.NewNoopResultDeliverer(),
			)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("human_review"))
		})
	})

	Describe("declines a non-draft PR when target is draft", func() {
		var (
			agent *agentlib.Agent
			ops   *mocks.GitOps
			gh    *mocks.GhCli
			gate  *mocks.GateRunner
		)

		BeforeEach(func() {
			ops = &mocks.GitOps{}
			gh = &mocks.GhCli{}
			gate = &mocks.GateRunner{}

			// non-draft PR
			gh.ViewPRReturns("OPEN", false, nil)
			ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
				if err := os.MkdirAll(workdir, 0o750); err != nil {
					return err
				}
				return os.WriteFile(
					filepath.Join(workdir, "CHANGELOG.md"),
					[]byte(changelogBranch),
					0o600,
				)
			}
			ops.ShowFileReturns([]byte(changelogMaster), nil)
			gate.RunTargetReturns("", 0, nil)
			ops.RevListReturns([]string{"deadbeef1", "deadbeef2"}, nil)
			ops.LsRemoteTagsReturns([]string{"1111111", "2222222"}, nil)

			agent = factory.CreateAgent(
				"", "", "", "tok", nil,
				ops, gh, gate,
				factory.CreateClaudeProber(""),
				updatepkg.PRTargetDraft,
			)
		})

		It("returns Failed for a mismatched non-draft PR under draft target", func() {
			result, err := agent.Run(
				ctx,
				domain.TaskPhaseAIReview,
				reviewTaskMD,
				delivery.NewNoopResultDeliverer(),
			)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.NextPhase).To(Equal(""))
		})
	})
})
