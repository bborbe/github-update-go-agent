# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- fix: harden planning/execution prompts with an explicit command-discipline block — run gate commands (`make check`/`make precommit`) to completion in the foreground, never background them or end the turn with "waiting for the background run"; final message must be exactly the required JSON, no prose before/after
- docs: document local-run gotchas (`-claude-config-dir=$HOME/.claude-agent`, unsetting session `ANTHROPIC_BASE_URL`/`ANTHROPIC_MODEL`/`ANTHROPIC_AUTH_TOKEN`) in README.md and cmd/run-task/README.md

## v0.2.0

- feat: implement planning phase — clone at ref, Claude gate-target/vuln classification, typed `## Plan`, park on unfixable findings naming the three suppression surfaces (design D4)
- feat: implement execution phase — custom Go step embedding a git/gh-less Claude update+repair sub-call, deterministic gate re-run, workflow-edit guard, bot-identity commit, `--no-follow-tags` push, `gh pr create --draft`, typed `## Result` with replay/PR-adopt idempotency guards
- feat: implement ai_review phase — pure-Go verifier (PR open+draft, fresh-worktree gate re-run, CHANGELOG `## Unreleased` + no new version header, no tag at branch commits), typed `## Review`, human_review routing on success only
- feat: add GitOps/GhCli/GateRunner seams, GitHub App IAT auth (APP_ID/INSTALLATION_ID/PEM) with raw GH_TOKEN fallback, claude-auth + gh-token preflights
- feat: replace template prompts with planning/execution phase prompts embedding the execution repair playbook
- feat: extend runtime image with Go toolchain, git, gh, make, jq, column, and trivy for in-pod repo gates

## v0.1.0

- feat: scaffold github-update-go-agent from bborbe/agent-claude template — module rename, design doc (docs/design.md)
