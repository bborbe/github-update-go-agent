# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- feat: add PR_TARGET setting (draft | ready, default draft) selecting whether the agent opens draft or ready-for-review pull requests; GhCli.CreateDraftPR renamed to CreatePR and parameterized on the target
- feat: add PRTarget value type (draft | ready) with ParsePRTarget defaulting to draft when unset
- feat: ai_review compares the pull request's observed draft-ness against the configured PR_TARGET instead of requiring draft unconditionally; a mismatch declines with a note naming both the observed and the configured state, while checks.pr_draft keeps reporting raw observed draft-ness
- docs: README, design doc and review-output comments now describe the configurable PR_TARGET (draft default | ready) instead of asserting the agent only ever opens drafts; the design doc's §7.0 capability-removal record carries a dated reversal note, and the merge and flip prohibitions are restated explicitly

## v0.3.1

- fix: bump `golang.org/x/mod` from `v0.37.0` to `v0.40.0` — clears GO-2026-6179 (transparency-log tile verification bypass in `sumdb/tlog`) and GO-2026-6180 (unrelated unauthenticated hashes accepted in `sumdb` Lookup), both flagged by `make vulncheck`
- chore: bump go directive from `1.26.5` to `1.26.6` — clears 4 stdlib advisories flagged by `osv-scanner` (GO-2026-5026, GO-2026-5972, GO-2026-6090, GO-2026-6218), all fixed in 1.26.6. `make precommit` was red on master before this change

## v0.3.0

- feat: installation-scope allowlist preflight (F9) — planning now checks the repo against the GitHub App installation's repository list (`gh api /installation/repositories`) before cloning and parks NeedsInput naming the allowlist when the repo is outside it; previously an out-of-scope task (dev App is repo-scoped by design) burned a full update run and only failed at `git push` (dev runs on bborbe/lock and bborbe/argument, 2026-07-21); unknown verdicts (local GH_TOKEN PAT fallback, API errors) never deny — the check fails open and push remains the enforcement backstop

## v0.2.4

- fix: add `gcc` + `musl-dev` to the runtime image so repo gates can run `go test -race` (requires cgo) — dev run #4 (`bborbe/lock`) parked with `go: -race requires cgo; enable cgo by setting CGO_ENABLED=1` because the alpine runtime stage had no C toolchain; verified `go test -race` passes on alpine/musl with these two packages

## v0.2.3

- fix: prose-tolerant LLM JSON extraction — `parseJSONResponse` now tries three strategies (raw JSON, fenced ` ```json ` block, LAST balanced `{...}` block in the text) instead of feeding the whole response straight to `json.Unmarshal`; fixes dev run #2 where the planning Claude/MiniMax sub-call ended its final message with a prose paragraph followed by the correct JSON object on its own line, and failed with `parse planning output: unmarshal llm json response: invalid character 'T' looking for beginning of value` even after prompt hardening ("final message must be exactly JSON") — prompt hardening reduces but cannot eliminate this LLM behavior, so the parser now tolerates it; ported the 3-strategy approach from `github-releaser-agent` `pkg/prompts.ParseBumpVerdict`; extracted into shared `pkg/llmjson.go` used by both planning (`PlanOutput`) and execution (`executionReport`) sub-call parsing

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
