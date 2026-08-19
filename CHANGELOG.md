# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased

- docs: list `AUTO_MERGE_LABEL` in design.md § 5.1 Inputs and § 5.2 Outputs — it was documented in the README env table but missing from the design doc's input/output contract

## v0.9.2

- fix: `make build` refuses to stamp a version onto a tree that is not that version's tag (`check-version-tag`, escape hatch `ALLOW_UNTAGGED_BUILD=1`). The image publish is operator-run and `VERSION` defaults to the newest tag, so a build started before the tag lands — or with an explicit `VERSION=` for a tag that does not exist yet — silently stamps new-version metadata onto old code. This drifted twice in one day (2026-08-19): the `v0.9.0` image was built from a stale tree, and the `v0.9.1` image was pushed at 09:25Z from a binary built 09:20Z, while the `v0.9.1` tag was only cut at 09:39Z — so the published `v0.9.1` image did not contain the prompt fix that `v0.9.1` exists to ship. Nothing surfaced it: the tag, the changelog and the image name all agreed, and only grepping the image binary showed the fix absent.

## v0.9.1

- fix: the execution prompt's CHANGELOG step now (a) marks the bullet MANDATORY when the repo has a CHANGELOG.md, and (b) ties its wording to the update scope. Two real defects on the 2026-08-18 deps sweep: `bborbe/badgerkv` merged a dep bump with no `## Unreleased` entry, so the `autoRelease` releaser never cut a version and consumers never saw it; and `bborbe/kafka-topic-purger` shipped `update Go to 1.26.6 and update dependencies` on a diff containing zero go-directive changes — a false claim in a released changelog. The step now spells out the per-scope wording (`golang` / `deps` / `both`) and forbids mentioning the Go version on a deps-scope run.

## v0.9.0

- feat: apply an optional `AUTO_MERGE_LABEL` to every PR at creation (`gh pr create --label <value>`) so a deployment can opt its agent PRs into GitHub-native auto-merge. Empty (default) behaves exactly as before. Never-merge boundary held: the agent only labels, it never calls `gh pr merge`.

## v0.8.0

- feat: ai_review writes a ## Your Move operator-action block at the top of a human_review-routed task body — a clickable PR link, the merge action, and a plain-text change summary (Go version bump and/or dependency/vulnerability updates) — so the operator can act without reading the ## Plan / ## Result / ## Review JSON

## v0.7.0

- feat: add `update_scope` knob (`golang` | `deps` | `both`, default `both`). The per-task frontmatter `update_scope` (or the `UPDATE_SCOPE` env deployment default) selects what the update sequence touches: golang-only skips the bulk dep update and filters dep work out of `has_work`; deps-only skips the go-directive bump. Unset behaves exactly as the previous release.

## v0.6.0

- chore: bump `github.com/bborbe/agent` v0.79.0 -> v0.81.3 — carries the retry-vs-escalate fix: `failed` results preserve `assignee` so `trigger_count`/`max_triggers` retries fire, and escalation (assignee cleared + `previous_assignee`) happens only at cap exhaustion (spec 010/021/027).
## v0.5.1

- fix: planning captures and parses the repo's own gate scanner output in Go, validates every plan advisory ID verbatim against the captured table, and carries the verbatim scanner row on park escalations
- fix: planning refutes false workdir/sandbox/permission needs_input claims with a Go stat before they can clear the assignee, and logs planning sub-call tool_result bodies (token-redacted) at the deployed log level

## v0.5.0

- fix: run the bulk dependency update (`go get -u ./...` + `go mod tidy`) deterministically in Go before the execution model call, instead of instructing the model to run it. Left to the model, a long `go get` invited backgrounding: on 2026-08-16 an execution run put it in a harness background task, blocked on `TaskOutput` for 600s, timed out, and re-issued the **identical blocking call on the same `task_id`** — three rounds consumed the Job's whole 1800s `activeDeadlineSeconds` and it was killed having produced nothing (`bborbe/ip` job `bc3c6599`; also `bborbe/run`, `bborbe/beactive`). Raising the deadline cannot fix it — waiting again is the bug. Each command now runs under a hard 8-minute timeout with no retry. Same reasoning that moved the ast-grep funnel into Go in `github-pr-review-agent`: when a model can express a step in many forms, remove the step from the model rather than constraining the forms.
- fix: the anti-backgrounding rule in both prompts named only the shell forms (`&`, `nohup`, detached jobs), so the harness's own `run_in_background` / `TaskOutput` read as permitted — the very forms that caused the outages. Both are now named explicitly, with the failure described so the rule is not mistaken for boilerplate.
- fix: the bulk-update loop checks `ctx.Done()` between commands, and each subprocess is logged on start as well as on completion. The start line is the point: this step exists because a long `go get` once hung invisibly, and a run that never returns must be distinguishable from one that was never attempted. Elapsed time is left to glog's per-line timestamps rather than a `time.Now()` read, keeping the package free of direct clock access.
- feat: `BulkUpdateResult` is fail-closed — a tooling failure comes back as `Ran=false` plus a reason, which the prompt surfaces as **"Bulk update — DID NOT RUN"** with instructions to run it in the foreground. The model is never left to assume the dependency graph was updated.

## v0.4.1

- fix: bump the Dockerfile build stage from `golang:1.26.5` to `golang:1.26.6` — v0.3.1 raised the `go.mod` directive to `1.26.6` but left the base image behind, so every image build since has failed at `go build` with `go.mod requires go >= 1.26.6 (running go 1.26.5; GOTOOLCHAIN=local)`. Nothing surfaced it because the image publish is a manual step: v0.3.1 and v0.4.0 both cut git tags with no corresponding Docker image, and the breakage only appeared at mirror time as `docker.io/bborbe/github-update-go-agent:v0.4.0: not found`

## v0.4.0

- feat: add PR_TARGET setting (draft | ready, default draft) selecting whether the agent opens draft or ready-for-review pull requests; GhCli.CreateDraftPR renamed to CreatePR and parameterized on the target
- feat: add PRTarget value type (draft | ready) with ParsePRTarget defaulting to draft when unset
- feat: ai_review compares the pull request's observed draft-ness against the configured PR_TARGET instead of requiring draft unconditionally; a mismatch declines with a note naming both the observed and the configured state, while checks.pr_draft keeps reporting raw observed draft-ness
- chore: gitignore dark-factory runtime artifacts (`.dark-factory.lock`, `.dark-factory.log`, `prompts/log/`, `specs/log/`) and untrack the ones a daemon run had swept into a commit — matches the convention in the dark-factory repo itself, which tracks only `prompts/completed/` and `specs/`
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
