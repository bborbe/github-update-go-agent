// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	libkafka "github.com/bborbe/kafka"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
		)
		Expect(agent).NotTo(BeNil())
	})
})
