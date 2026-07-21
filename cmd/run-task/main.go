// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command run-task is the local-CLI entry point for github-update-go-agent.
//
// Reads a markdown task file from disk, runs the agent against it, and
// writes the updated content back to the same file. Mirrors the Kafka
// entry point (../../main.go) but uses file I/O instead of Kafka/CQRS,
// accepts a raw GH_TOKEN (the operator's `gh auth token`), and never runs
// `gh auth setup-git` — the developer's existing credential setup stays
// untouched.
//
// Used for local development and integration testing without spinning up
// a K8s Job + Kafka cluster.
package main

import (
	"context"
	"os"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/agent/envparse"
	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/errors"
	libsentry "github.com/bborbe/sentry"
	"github.com/bborbe/service"
	"github.com/bborbe/vault-cli/pkg/domain"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/factory"
)

func main() {
	app := &application{}
	os.Exit(service.Main(context.Background(), app, &app.SentryDSN, &app.SentryProxy))
}

type application struct {
	SentryDSN   string `required:"false" arg:"sentry-dsn"   env:"SENTRY_DSN"   usage:"SentryDSN"    display:"length"`
	SentryProxy string `required:"false" arg:"sentry-proxy" env:"SENTRY_PROXY" usage:"Sentry Proxy" display:"length"`

	// Claude Code CLI configuration
	ClaudeConfigDir claudelib.ClaudeConfigDir `required:"false" arg:"claude-config-dir" env:"CLAUDE_CONFIG_DIR" usage:"Claude Code config directory"`

	// Agent directory (contains .claude/ with CLAUDE.md and commands)
	AgentDir claudelib.AgentDir `required:"false" arg:"agent-dir" env:"AGENT_DIR" usage:"Agent directory with .claude/ config" default:"agent"`

	// Environment variables passed to Claude CLI process (comma-separated KEY=VALUE pairs).
	ClaudeEnvRaw string `required:"false" arg:"claude-env" env:"CLAUDE_ENV" usage:"Comma-separated KEY=VALUE pairs for Claude CLI environment"`

	// Anthropic-compatible provider routing.
	AnthropicBaseURL   string                `required:"false" arg:"anthropic-base-url"   env:"ANTHROPIC_BASE_URL"   usage:"Anthropic-compatible API base URL"`
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"password"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                    default:"sonnet"`

	// Environment
	Branch base.Branch `required:"true" arg:"branch" env:"BRANCH" usage:"branch" default:"dev"`

	// Phase to run (defaults to planning for local iteration)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"planning"`

	// GitHub token — raw credential for the local path (operator's
	// `gh auth token`). App creds are the cluster path; locally the raw
	// token is accepted per design § 7.2.
	GhToken string `required:"true" arg:"gh-token" env:"GH_TOKEN" usage:"GitHub token for clone + PR creation" display:"length"`

	// Task file for local development
	TaskFilePath string `required:"true" arg:"task-file" env:"TASK_FILE" usage:"Path to the markdown task file"`
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	taskContent, err := os.ReadFile(
		a.TaskFilePath,
	) // #nosec G304 -- filePath from trusted CLI input
	if err != nil {
		return errors.Wrap(ctx, err, "read task file: "+a.TaskFilePath)
	}

	deliverer := factory.CreateFileResultDeliverer(a.TaskFilePath)

	claudeEnv := envparse.KeyValuePairs(a.ClaudeEnvRaw)
	if claudeEnv == nil {
		claudeEnv = map[string]string{}
	}
	for k, v := range updatepkg.BuildEnv(
		a.GhToken,
		a.AnthropicBaseURL,
		a.AnthropicAuthToken,
		a.AnthropicModel.String(),
	) {
		claudeEnv[k] = v
	}

	// The gate targets inherit os.Environ(); export the token so repo
	// Makefiles (private module fetches) see it. No `gh auth setup-git`
	// locally — the developer's gh login handles git credentials.
	if err := os.Setenv("GH_TOKEN", a.GhToken); err != nil {
		return errors.Wrap(ctx, err, "export GH_TOKEN")
	}

	agent := factory.CreateAgent(
		a.ClaudeConfigDir,
		a.AgentDir,
		a.AnthropicModel,
		a.GhToken,
		claudeEnv,
		factory.CreateGitOps(),
		factory.CreateGhCli(a.GhToken),
		factory.CreateGateRunner(),
		factory.CreateClaudeProber(a.ClaudeConfigDir),
	)

	result, err := agent.Run(ctx, a.Phase, string(taskContent), deliverer)
	if err != nil {
		return errors.Wrap(ctx, err, "agent run failed")
	}
	return agentlib.PrintResult(ctx, result)
}
