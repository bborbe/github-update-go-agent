# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.2.2

- fix: execution step no-effective-change guard — when the changed-files set after the Claude update sub-call is empty or contains only `CHANGELOG.md`, write `## Result` outcome=`no_update_needed` and route to `done` instead of committing/pushing/opening a draft PR; fixes the go-skeleton PR #51 incident where planning classified `has_work: true` off stale INDIRECT deps but `go get -u ./...` + `go mod tidy` no-oped under MVS, leaving only a fabricated CHANGELOG bullet
- chore: bump `github.com/bborbe/agent` to v0.72.0 → v0.79.0 (deliverer: `Status:Done` + empty `NextPhase` is now an in-place save, not task completion — prevents transient premature task completion from preflight publishes) — audited all `AgentStatusDone` returns; every phase-terminal path (planning `no_update_needed`/`ready`, execution success x3 incl. the new no-effective-change guard, review approved) already emits an explicit `NextPhase`, so no behavior change was required

## v0.2.1

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
