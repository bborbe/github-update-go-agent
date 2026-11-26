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
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/factory"
	"github.com/bborbe/github-update-go-agent/pkg/maintainerconfig"
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
	AnthropicAuthToken string                `required:"false" arg:"anthropic-auth-token" env:"ANTHROPIC_AUTH_TOKEN" usage:"Bearer token for ANTHROPIC_BASE_URL"                                  display:"length"`
	AnthropicModel     claudelib.ClaudeModel `required:"false" arg:"anthropic-model"      env:"ANTHROPIC_MODEL"      usage:"Model name; also exposed to the claude subprocess as ANTHROPIC_MODEL"                  default:"sonnet"`

	// Environment
	Branch base.Branch `required:"true" arg:"branch" env:"BRANCH" usage:"branch" default:"dev"`

	// Phase to run (defaults to planning for local iteration)
	Phase domain.TaskPhase `required:"false" arg:"phase" env:"PHASE" usage:"Agent phase: planning | execution | ai_review" default:"planning"`

	// GitHub token — raw credential for the local path (operator's
	// `gh auth token`). App creds are the cluster path; locally the raw
	// token is accepted per design § 7.2.
	GhToken string `required:"true" arg:"gh-token" env:"GH_TOKEN" usage:"GitHub token for clone + PR creation" display:"length"`

	// PRTarget selects how the agent opens pull requests: draft (default)
	// or ready. Unset behaves exactly as the previous release: drafts only.
	PRTarget string `required:"false" arg:"pr-target" env:"PR_TARGET" usage:"Pull request target at creation: draft (default) | ready"`

	// AutoMergeLabel, when non-empty, is applied to every PR this agent opens
	// (gh pr create --label <value>), opting it into GitHub-native auto-merge.
	// Empty (default) adds no label. Never-merge boundary held: label only,
	// never gh pr merge.
	AutoMergeLabel string `required:"false" arg:"auto-merge-label" env:"AUTO_MERGE_LABEL" usage:"PR label applied at creation to opt into GitHub-native auto-merge (e.g. 'auto-merge'); empty disables"`

	// UpdateScope selects what the update sequence touches: both (default),
	// golang, or deps. The per-task frontmatter `update_scope` overrides this
	// deployment default. Unset behaves exactly as the previous release.
	UpdateScope string `required:"false" arg:"update-scope" env:"UPDATE_SCOPE" usage:"Update scope: both (default) | golang | deps"`

	// Task file for local development. Optional only because --dry-run needs
	// no task: Run enforces it for every non-dry-run invocation.
	TaskFilePath string `required:"false" arg:"task-file" env:"TASK_FILE" usage:"Path to the markdown task file (required unless --dry-run)"`

	// DryRun resolves the target repo's default branch and exits before any
	// write — no clone, no branch, no commit, no push, no PR. Requires Repo.
	DryRun bool `required:"false" arg:"dry-run" env:"DRY_RUN" usage:"Resolve --repo's default branch and exit before any write"`

	// Repo is the owner/name target for --dry-run.
	Repo string `required:"false" arg:"repo" env:"REPO" usage:"owner/name target for --dry-run"`

	// TaskType selects which domain agent to run: github-update-go (default)
	// or build-fix. Mirrors the Kafka entry point's TASK_TYPE dispatch.
	TaskType string `required:"false" arg:"task-type" env:"TASK_TYPE" usage:"Task type: github-update-go (default) | build-fix" default:"github-update-go"`

	// Fleet scan scope (task_type github-fleet-scan).
	Owner         string `required:"false" arg:"owner"          env:"OWNER"          usage:"GitHub owner whose repositories the fleet scan enumerates"`
	RepoAllowlist string `required:"false" arg:"repo-allowlist" env:"REPO_ALLOWLIST" usage:"Comma-separated host/owner/repo allowlist; '*' wildcard, '!' prefix excludes"`
	Stage         string `required:"false" arg:"stage"          env:"STAGE"          usage:"Stage stamped on every task the fleet scan files"`

	// HumanReviewOwner is the GitHub login stamped as the assignee on every record
	// routed into human_review. Empty disables the stamp — the local replay then
	// behaves exactly as before this change.
	HumanReviewOwner string `required:"false" arg:"human-review-owner" env:"HUMAN_REVIEW_OWNER" usage:"GitHub login stamped as assignee on every record routed to human_review; empty disables the stamp"`
}

// runDryRun resolves the target repo's default branch and returns without
// writing anything: no clone, no branch, no commit, no push, no PR. The
// resolver emits its own `default=refs/heads/<branch>` V(2) line (run with
// -v=2); the summary line here makes the outcome observable without it.
func (a *application) runDryRun(ctx context.Context) error {
	if a.Repo == "" {
		return errors.Errorf(ctx, "run-task --dry-run: --repo owner/name is required")
	}
	branch, headSHA, err := updatepkg.DryRunResolve(
		ctx, factory.CreateGitOps(), a.GhToken, a.Repo,
	)
	if err != nil {
		return errors.Wrap(ctx, err, "dry-run resolve default branch")
	}
	glog.V(0).Infof(
		"dry-run: repo=%s default=%s head=%s (no write performed)",
		a.Repo,
		branch,
		headSHA,
	)
	return nil
}

// buildFleetScanDeps assembles the nightly scan's dependency struct for the
// local CLI path, using the operator's raw token.
func (a *application) buildFleetScanDeps() updatepkg.FleetScanDeps {
	return updatepkg.FleetScanDeps{
		Config: updatepkg.FleetScanConfig{
			Owner:     a.Owner,
			Allowlist: updatepkg.ParseRepoAllowlist(a.RepoAllowlist),
			Stage:     a.Stage,
		},
		GhToken:         a.GhToken,
		Lister:          updatepkg.NewGitHubRepoLister(a.GhToken),
		Files:           updatepkg.NewGitHubRepoFileFetcher(a.GhToken),
		Consent:         maintainerconfig.NewHTTPFetcher(a.GhToken),
		GitOps:          factory.CreateGitOps(),
		Gate:            factory.CreateGateRunner(),
		Classifier:      updatepkg.NewOSVIgnoreClassifier(updatepkg.NewOSVHTTPFetcher()),
		CurrentDateTime: libtime.NewCurrentDateTime(),
	}
}

func (a *application) Run(ctx context.Context, _ libsentry.Client) error {
	if a.DryRun {
		return a.runDryRun(ctx)
	}
	if a.TaskFilePath == "" {
		return errors.Errorf(ctx, "run-task: --task-file is required unless --dry-run")
	}
	prTarget, err := updatepkg.ParsePRTarget(ctx, a.PRTarget)
	if err != nil {
		return err
	}
	updateScope, err := updatepkg.ParseUpdateScope(ctx, a.UpdateScope)
	if err != nil {
		return err
	}
	// Same warning as the pod entry point, from the same factory function, so a
	// local replay with the value unset does not silently compose no wrapper.
	if warning := factory.HumanReviewOwnerWarning(a.HumanReviewOwner); warning != "" {
		glog.Warning(warning)
	}

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
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		claudeEnv[k] = v
	}

	// The gate targets inherit os.Environ(); export the token so repo
	// Makefiles (private module fetches) see it. No `gh auth setup-git`
	// locally — the developer's gh login handles git credentials.
	if err := os.Setenv("GH_TOKEN", a.GhToken); err != nil {
		return errors.Wrap(ctx, err, "export GH_TOKEN")
	}

	provider := factory.CreateAgentProvider(
		a.ClaudeConfigDir,
		a.AgentDir,
		a.AnthropicModel,
		a.GhToken,
		claudeEnv,
		factory.CreateGitOps(),
		factory.CreateGhCli(a.GhToken),
		factory.CreateGateRunner(),
		factory.CreateClaudeProber(a.ClaudeConfigDir),
		prTarget,
		a.AutoMergeLabel,
		updateScope,
		// createCmd: nil in local mode — chain emission records a no-op so
		// a build-fix replay still exercises the full phase path.
		nil,
		factory.ResolveHumanReviewOwner(map[string]string{
			factory.HumanReviewOwnerEnv: a.HumanReviewOwner,
		}),
		a.buildFleetScanDeps(),
	)
	agent, err := provider.Get(ctx, agentlib.TaskType(a.TaskType))
	if err != nil {
		return errors.Wrap(ctx, err, "select agent for task_type")
	}

	result, err := agent.Run(ctx, a.Phase, string(taskContent), deliverer)
	if err != nil {
		return errors.Wrap(ctx, err, "agent run failed")
	}
	return agentlib.PrintResult(ctx, result)
}
