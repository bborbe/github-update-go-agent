// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package factory wires concrete dependencies for the github-update-go-agent binary.
//
// All factory functions follow the Create* prefix convention and contain
// zero business logic — they compose constructors with config.
package factory

import (
	"context"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	delivery "github.com/bborbe/agent/delivery"
	healthcheck "github.com/bborbe/agent/healthcheck"
	"github.com/bborbe/cqrs/base"
	libkafka "github.com/bborbe/kafka"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/git"
)

const serviceName = "github-update-go-agent"

// taskTypeGithubUpdateGo is the agent-lib TaskType literal for this agent's
// domain task. No constant exists in agent-lib for this value, so we cast it
// locally (mirrors github-dark-factory-agent). Keep the literal exactly
// "github-update-go" — the watcher emits it verbatim and the CRD
// trigger.task_type field must match.
var taskTypeGithubUpdateGo = agentlib.TaskType("github-update-go")

// planningTools is the planning phase's Claude tool scope: inspect-only —
// no Edit/Write, no push (design § 4.3 planning).
var planningTools = claudelib.AllowedTools{
	"Read", "Grep", "Glob",
	"Bash(git:*)", "Bash(go:*)", "Bash(make:*)",
}

// executionTools is the execution Claude sub-call's tool scope: file-edit +
// go/make only — NO git, NO gh. Every git/PR side effect is the Go step's
// (design § 7.0 capability removal).
var executionTools = claudelib.AllowedTools{
	"Read", "Grep", "Glob", "Edit", "Write",
	"Bash(go:*)", "Bash(make:*)",
}

// CreateClaudeRunner constructs a ClaudeRunner pre-configured with tools,
// model, working directory, and CLI environment.
func CreateClaudeRunner(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	allowedTools claudelib.AllowedTools,
	model claudelib.ClaudeModel,
	env map[string]string,
) claudelib.ClaudeRunner {
	return claudelib.NewClaudeRunner(claudelib.ClaudeRunnerConfig{
		ClaudeConfigDir:  claudeConfigDir,
		AllowedTools:     allowedTools,
		Model:            model,
		WorkingDirectory: agentDir,
		Env:              env,
	})
}

// CreateGitOps wires the os/exec GitOps seam.
func CreateGitOps() git.GitOps {
	return git.NewOSExecGitOps()
}

// CreateGhCli wires the os/exec gh CLI seam with the resolved GitHub token.
func CreateGhCli(ghToken string) updatepkg.GhCli {
	return updatepkg.NewOSExecGhCli(ghToken)
}

// CreateGateRunner wires the os/exec make gate runner.
func CreateGateRunner() updatepkg.GateRunner {
	return updatepkg.NewOSExecGateRunner()
}

// CreateClaudeProber wires the claude-auth preflight prober.
func CreateClaudeProber(claudeConfigDir claudelib.ClaudeConfigDir) updatepkg.ClaudeProber {
	return updatepkg.NewClaudeProber(claudeConfigDir)
}

// CreateSyncProducer creates a Kafka sync producer.
func CreateSyncProducer(
	ctx context.Context,
	brokers libkafka.Brokers,
) (libkafka.SyncProducer, error) {
	return libkafka.NewSyncProducerWithName(ctx, brokers, serviceName)
}

// CreateKafkaResultDeliverer creates a ResultDeliverer that publishes task
// updates to Kafka via CQRS commands. Uses the passthrough content generator
// — the agent framework's StepRunner already produces the full marshaled
// task in result.Output; the deliverer publishes it as-is and overrides
// status/phase frontmatter based on the result Status.
func CreateKafkaResultDeliverer(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
	taskID agentlib.TaskIdentifier,
	originalContent string,
	currentDateTime libtime.CurrentDateTimeGetter,
) agentlib.ResultDeliverer {
	return delivery.NewKafkaResultDeliverer(
		syncProducer,
		topicPrefix,
		taskID,
		originalContent,
		delivery.NewPassthroughContentGenerator(),
		currentDateTime,
	)
}

// CreateFileResultDeliverer creates a ResultDeliverer that writes the agent's
// output back to a markdown file (local CLI mode). Uses the passthrough
// content generator (same rationale as Kafka).
func CreateFileResultDeliverer(filePath string) agentlib.ResultDeliverer {
	return delivery.NewFileResultDeliverer(
		delivery.NewPassthroughContentGenerator(),
		filePath,
	)
}

// CreateAgent assembles the three distinct phases (design § 4.2):
//
//   - planning:  claude-auth + gh-token preflights + Claude planning step
//     (clone @ ref, detect gate targets, classify findings) → ## Plan
//   - execution: claude-auth preflight + custom Go step embedding the Claude
//     update sub-call (clone+branch, update+repair, gate verify, commit,
//     push --no-follow-tags, gh pr create --draft) → ## Result
//   - ai_review: pure-Go verifier (PR state, fresh-worktree gate re-run,
//     CHANGELOG, tag audit) → ## Review → human_review
func CreateAgent(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	claudeEnv map[string]string,
	gitOps git.GitOps,
	ghCli updatepkg.GhCli,
	gateRunner updatepkg.GateRunner,
	claudeProber updatepkg.ClaudeProber,
) *agentlib.Agent {
	claudeAuth := updatepkg.NewClaudeAuthStep(claudeProber)
	ghTokenCheck := updatepkg.NewGHTokenCheckStep(ghToken)
	planningRunner := CreateClaudeRunner(claudeConfigDir, agentDir, planningTools, model, claudeEnv)
	planningStep := updatepkg.NewPlanningStep(planningRunner, gitOps, ghToken)
	executionRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		executionTools,
		model,
		claudeEnv,
	)
	executionStep := updatepkg.NewExecutionStep(executionRunner, gitOps, ghCli, gateRunner, ghToken)
	reviewStep := updatepkg.NewReviewStep(gitOps, ghCli, gateRunner, ghToken)

	return agentlib.NewAgent(
		agentlib.NewPhase(domain.TaskPhasePlanning, claudeAuth, ghTokenCheck, planningStep),
		agentlib.NewPhase(domain.TaskPhaseExecution, claudeAuth, executionStep),
		agentlib.NewPhase(domain.TaskPhaseAIReview, reviewStep),
	)
}

// CreateAgentProvider wires the per-task-type dispatch table.
//   - task_type: github-update-go → the 3-phase domain agent
//   - task_type: healthcheck / oauth-probe → shared liveness agent
//
// Pure plumbing; no conditional, no error.
func CreateAgentProvider(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	claudeEnv map[string]string,
	gitOps git.GitOps,
	ghCli updatepkg.GhCli,
	gateRunner updatepkg.GateRunner,
	claudeProber updatepkg.ClaudeProber,
) agentlib.AgentProvider {
	domainAgent := CreateAgent(
		claudeConfigDir,
		agentDir,
		model,
		ghToken,
		claudeEnv,
		gitOps,
		ghCli,
		gateRunner,
		claudeProber,
	)
	healthcheckRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		claudelib.AllowedTools{},
		model,
		claudeEnv,
	)
	livenessAgent := healthcheck.NewAgent(healthcheck.NewClaudeStep(healthcheckRunner))
	return agentlib.NewAgentProvider(serviceName, map[agentlib.TaskType]*agentlib.Agent{
		taskTypeGithubUpdateGo:       domainAgent,
		agentlib.TaskTypeHealthcheck: livenessAgent,
		agentlib.TaskTypeOAuthProbe:  livenessAgent,
	})
}
