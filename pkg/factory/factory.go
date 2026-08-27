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
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	task "github.com/bborbe/agent/command/task"
	delivery "github.com/bborbe/agent/delivery"
	healthcheck "github.com/bborbe/agent/healthcheck"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/cqrs/cdb"
	"github.com/bborbe/errors"
	libkafka "github.com/bborbe/kafka"
	"github.com/bborbe/log"
	libtime "github.com/bborbe/time"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"
	"github.com/google/uuid"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/git"
	"github.com/bborbe/github-update-go-agent/pkg/maintainerconfig"
)

const serviceName = "github-update-go-agent"

// taskTypeGithubUpdateGo is the agent-lib TaskType literal for this agent's
// domain task. No constant exists in agent-lib for this value, so we cast it
// locally (mirrors github-dark-factory-agent). Keep the literal exactly
// "github-update-go" — the watcher emits it verbatim and the CRD
// trigger.task_type field must match.
var taskTypeGithubUpdateGo = agentlib.TaskType("github-update-go")

// taskTypeBuildFix is the agent-lib TaskType literal for the build-fix
// domain task (the second task type this binary hosts, per the 2026-08-22
// architecture decision). Keep the literal exactly "build-fix" — the
// github-build watcher emits it verbatim and the CRD trigger.task_type field
// must match.
var taskTypeBuildFix = agentlib.TaskType("build-fix")

// readOnlyShellTools are the read-only text utilities both phases may shell
// out to. They exist because the model reaches for shell pipelines even when
// told to prefer the Grep/Read tools, and a denied Bash call is not a cheap
// failure: the model retries it until the Job's activeDeadlineSeconds is
// exhausted, discarding work that already passed its gates (observed
// 2026-08-16, bborbe/ip, 1800s burned after gate_exit: 0).
//
// Every stage of a pipeline must be allowlisted independently — Claude Code
// splits `go mod graph | grep foo` into two operations and denies the whole
// command if either half is unlisted. That is why `grep` alone is not enough;
// the paging tails the model habitually appends need listing too.
//
// Strictly read-only by construction: nothing here mutates the workdir, so
// widening the scope does not widen the blast radius. Write access stays with
// Edit/Write, and git/gh remain absent from execution.
var readOnlyShellTools = []string{
	"Bash(grep:*)", "Bash(head:*)", "Bash(tail:*)",
	"Bash(cat:*)", "Bash(wc:*)", "Bash(sort:*)", "Bash(uniq:*)",
}

// planningTools is the planning phase's Claude tool scope: inspect-only —
// no Edit/Write, no push (design § 4.3 planning).
var planningTools = append(
	claudelib.AllowedTools{
		"Read", "Grep", "Glob",
		"Bash(git:*)", "Bash(go:*)", "Bash(make:*)",
	},
	readOnlyShellTools...,
)

// executionTools is the execution Claude sub-call's tool scope: file-edit +
// go/make only — NO git, NO gh. Every git/PR side effect is the Go step's
// (design § 7.0 capability removal).
var executionTools = append(
	claudelib.AllowedTools{
		"Read", "Grep", "Glob", "Edit", "Write",
		"Bash(go:*)", "Bash(make:*)",
	},
	readOnlyShellTools...,
)

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

// CreateBuildFixChainEmitter wires the build-fixer's chain-to-updater
// emission: a CreateCommandFunc that publishes a github-update-go task via
// the controller's Kafka command bus (task.CreateCommandSender), exactly as
// the github-build watcher does for build-fix tasks. The producer is passed
// in pre-built — main.go owns its lifecycle (created + closed there) and its
// nil decision (buildChainEmitter returns a nil createCmd when no brokers are
// configured), so the factory body stays pure composition with no error and
// no conditional. The Pattern B Job emits at most one downstream task per run.
func CreateBuildFixChainEmitter(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) updatepkg.CreateCommandFunc {
	return func(
		ctx context.Context,
		repo, episodeSHA string,
		workflows []string,
	) error {
		defer closeChainProducer(syncProducer)
		sender := cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory)
		createSender := task.NewCreateCommandSender(sender, "")
		cmd := buildChainCommand(repo, episodeSHA, workflows)
		if err := createSender.SendCommand(ctx, cmd); err != nil {
			return errors.Wrap(ctx, err, "send github-update-go chain task")
		}
		return nil
	}
}

// closeChainProducer closes the chain emitter's sync producer, warning on
// failure. Extracted as a named func so the emitter closure contains no
// conditional (factory no-conditional-in-body rule).
func closeChainProducer(producer libkafka.SyncProducer) {
	if err := producer.Close(); err != nil {
		glog.Warningf("close chain sync producer failed: %v", err)
	}
}

// buildChainCommand assembles the chained github-update-go task. The
// clone_url frontmatter is set only for owner/repo inputs, so the updater's
// HTTPS auth path has the explicit git@ remote.
func buildChainCommand(repo, episodeSHA string, workflows []string) task.CreateCommand {
	fm := chainFrontmatter(repo, episodeSHA)
	return task.CreateCommand{
		Title:          "Update Go " + repo + " at " + shortSHA(episodeSHA),
		TaskIdentifier: agentlib.TaskIdentifier(deriveUpdateGoTaskID(repo, episodeSHA)),
		Frontmatter:    fm,
		Body:           buildChainBody(repo, episodeSHA, workflows),
	}
}

// chainFrontmatter builds the chained task's frontmatter, adding clone_url
// for owner/repo inputs so the updater's HTTPS auth path has the explicit
// git@ remote.
func chainFrontmatter(repo, episodeSHA string) agentlib.TaskFrontmatter {
	fm := agentlib.TaskFrontmatter{
		"task_type":   "github-update-go",
		"assignee":    "github-update-go-agent",
		"repo":        repo,
		"episode_sha": episodeSHA,
		"status":      "in_progress",
		"phase":       "planning",
	}
	if len(splitRepo(repo)) == 2 {
		fm["clone_url"] = "git@github.com:" + repo + ".git"
	}
	return fm
}

// updateGoChainNamespace is the fixed v5 UUID namespace for chained
// github-update-go task identifiers (distinct from the build watcher's own
// namespace so the two services cannot collide).
var updateGoChainNamespace = uuid.MustParse("6f4d2b1a-9c3e-4d7a-b5f0-2a8c1d3e5f6a")

// deriveUpdateGoTaskID produces a deterministic task identifier for the
// chained github-update-go task: UUID5 of (owner/repo, episode SHA), matching
// the build watcher's DeriveTaskID shape. The updater's own task identifiers
// use (owner, repo, head_sha); here the episode SHA stands in as the ref the
// updater will clone at, so a re-diagnosis of the same episode chains to the
// same updater task (dedup).
func deriveUpdateGoTaskID(repo, episodeSHA string) string {
	key := repo + "#build-chain-" + episodeSHA
	return uuid.NewSHA1(updateGoChainNamespace, []byte(key)).String()
}

// buildChainBody renders the chained github-update-go task body: header +
// the failing-workflow evidence so the updater has context for why it was
// dispatched.
func buildChainBody(repo, episodeSHA string, workflows []string) string {
	b := "Chained by the build-fix agent: the CI episode " + episodeSHA +
		" for " + repo + " was diagnosed as a stale-dependency / vulnerability " +
		"failure. Update the repo's Go toolchain + dependencies.\n\n"
	b += "Episode SHA: `" + episodeSHA + "`\n\n"
	if len(workflows) > 0 {
		b += "## Failing Workflows\n\n"
		for _, w := range workflows {
			b += "- " + w + "\n"
		}
		b += "\n"
	}
	return b
}

// splitRepo splits "owner/name" into [owner, name]; returns nil on malformed.
func splitRepo(repo string) []string {
	parts := strings.Split(repo, "/")
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts
	}
	return nil
}

// shortSHA bounds an episode SHA to its 7-char prefix.
func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

// CreateAgent assembles the three distinct phases (design § 4.2):
//
//   - planning:  claude-auth + gh-token preflights + Claude planning step
//     (clone @ ref, detect gate targets, classify findings) → ## Plan
//   - execution: claude-auth preflight + custom Go step embedding the Claude
//     update sub-call (clone+branch, update+repair, gate verify, commit,
//     push --no-follow-tags, gh pr create) → ## Result
//   - ai_review: pure-Go verifier (PR state checked against configured
//     PRTarget, fresh-worktree gate re-run, CHANGELOG, tag audit) → ## Review → human_review
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
	prTarget updatepkg.PRTarget,
	autoMergeLabel string,
	updateScope updatepkg.UpdateScope,
) *agentlib.Agent {
	claudeAuth := updatepkg.NewClaudeAuthStep(claudeProber)
	ghTokenCheck := updatepkg.NewGHTokenCheckStep(ghToken)
	planningRunner := updatepkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{
		ClaudeConfigDir:  claudeConfigDir,
		AllowedTools:     planningTools,
		Model:            model,
		WorkingDirectory: agentDir,
		Env:              claudeEnv,
	})
	planningStep := updatepkg.NewPlanningStep(
		planningRunner,
		gitOps,
		gateRunner,
		ghToken,
		updatepkg.NewGhInstallationScope(ghToken),
		maintainerconfig.NewHTTPFetcher(ghToken),
		updateScope,
	)
	executionRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		executionTools,
		model,
		claudeEnv,
	)
	executionStep := updatepkg.NewExecutionStep(
		executionRunner,
		gitOps,
		ghCli,
		gateRunner,
		updatepkg.NewBulkUpdater(),
		ghToken,
		prTarget,
		autoMergeLabel,
		updateScope,
	)
	reviewStep := updatepkg.NewReviewStep(gitOps, ghCli, gateRunner, ghToken, prTarget)

	return agentlib.NewAgent(
		agentlib.NewPhase(domain.TaskPhasePlanning, claudeAuth, ghTokenCheck, planningStep),
		agentlib.NewPhase(domain.TaskPhaseExecution, claudeAuth, executionStep),
		agentlib.NewPhase(domain.TaskPhaseAIReview, reviewStep),
	)
}

// CreateBuildFixAgent assembles the build-fixer's three phases (the second
// domain agent this binary hosts, per the 2026-08-22 architecture decision
// — shares the binary's core plumbing, no separate repo/deployment):
//
//   - planning:  claude-auth preflight + fix diagnosis step (clone @ episode
//     SHA, verify green at HEAD, fetch failed logs, Claude classification
//     into no_fix_needed / chain_update / file_spec / needs_input) → ## Fix Plan
//   - execution: pure-Go hand-off step — chain_update emits a github-update-go
//     task (CreateCommand), file_spec files the kind:bug spec on
//     build-fixer/<sha-short>, dedup via branch existence → ## Fix Result
//   - ai_review: pure-Go verifier (branch landed on origin / hand-off
//     recorded) → ## Review → human_review
func CreateBuildFixAgent(
	claudeConfigDir claudelib.ClaudeConfigDir,
	agentDir claudelib.AgentDir,
	model claudelib.ClaudeModel,
	ghToken string,
	claudeEnv map[string]string,
	gitOps git.GitOps,
	ghCli updatepkg.GhCli,
	claudeProber updatepkg.ClaudeProber,
	createCmd updatepkg.CreateCommandFunc,
) *agentlib.Agent {
	claudeAuth := updatepkg.NewClaudeAuthStep(claudeProber)
	fixRunner := CreateClaudeRunner(
		claudeConfigDir,
		agentDir,
		planningTools,
		model,
		claudeEnv,
	)
	planningStep := updatepkg.NewFixPlanningStep(
		fixRunner,
		gitOps,
		ghCli,
		ghToken,
	)
	executionStep := updatepkg.NewFixExecutionStep(
		gitOps,
		ghCli,
		ghToken,
		createCmd,
	)
	reviewStep := updatepkg.NewFixReviewStep(gitOps, ghToken)

	return agentlib.NewAgent(
		agentlib.NewPhase(domain.TaskPhasePlanning, claudeAuth, planningStep),
		agentlib.NewPhase(domain.TaskPhaseExecution, executionStep),
		agentlib.NewPhase(domain.TaskPhaseAIReview, reviewStep),
	)
}

// CreateAgentProvider wires the per-task-type dispatch table.
//   - task_type: github-update-go → the 3-phase updater agent
//   - task_type: build-fix → the 3-phase build-fixer agent (second domain
//     agent in this binary, per the 2026-08-22 architecture decision)
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
	prTarget updatepkg.PRTarget,
	autoMergeLabel string,
	updateScope updatepkg.UpdateScope,
	createCmd updatepkg.CreateCommandFunc,
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
		prTarget,
		autoMergeLabel,
		updateScope,
	)
	fixAgent := CreateBuildFixAgent(
		claudeConfigDir,
		agentDir,
		model,
		ghToken,
		claudeEnv,
		gitOps,
		ghCli,
		claudeProber,
		createCmd,
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
		taskTypeBuildFix:             fixAgent,
		agentlib.TaskTypeHealthcheck: livenessAgent,
		agentlib.TaskTypeOAuthProbe:  livenessAgent,
	})
}
