# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## v0.14.0

- feat: ai_review on an already-merged PR routes `done` instead of `human_review` — when the update PR has shipped (MERGED state), the task auto-completes with no `## Your Move` block and no manual close, closing the merge-detection tail on the agent side

## v0.13.1

- fix: the execution step's forbidden-workflow-path guard now classifies `.github/workflows/*` changes instead of unconditionally refusing — a maintainer dep-bump's deterministic regeneration (content differs from origin/master base) is committed, a byte-identical no-op regeneration is skipped, and a brand-new workflow file (no base version) still refuses. The model is architecturally forbidden from editing workflows (no git/gh tools, prompt prohibition, App lacks Workflows permission — design D3), so a workflow change at commit time can only be the update's own regeneration.

## v0.13.0

- feat: publish agent image to Docker Hub on tag push — builds at the exact tag so the embedded version always matches (version-true by construction), closing the recurring "released tag has no image" gap

## v0.12.10

- chore: update github.com/bborbe/agent to v0.83.1, github.com/bborbe/errors to v1.5.21, github.com/bborbe/log to v1.6.25, github.com/bborbe/maintainer to v0.50.3

## v0.12.9

- chore: update github.com/bborbe/agent to v0.83.0, github.com/bborbe/cqrs to v0.6.8, github.com/bborbe/errors to v1.5.20, github.com/bborbe/kafka to v1.25.9, github.com/bborbe/log to v1.6.24, github.com/bborbe/maintainer to v0.50.2, github.com/bborbe/sentry to v1.9.27, github.com/bborbe/service to v1.10.9, github.com/bborbe/time to v1.27.10, github.com/bborbe/vault-cli to v0.116.2, github.com/onsi/ginkgo/v2 to v2.32.1, github.com/prometheus/client_golang to v1.24.1

## v0.12.8

- chore: update Go to 1.27.0

## v0.12.7

- fix: planning's plan-validation is now suppression-synced — `filterSuppressedVulns` drops operator-approved no-fix IDs from the plan's vulns before `validatePlanAgainstTable`, mirroring the scanner-table filter from v0.12.6. The model can still echo a suppressed ID (it appears in the task body's prior `## Failure` text even though it is absent from the filtered scanner table); validating such a plan against the filtered table hard-failed with `plan validation: vuln id ... not found in captured scanner output` (observed 2026-08-24 on hue), replacing the fixed re-park with a new failure.

## v0.12.6

- fix: planning's scanner table is now suppression-aware — `loadSuppressedVulnIDs` reads the repo's three fleet-convention suppression surfaces (`.osv-scanner.toml` `[[IgnoredVulns]]`, `.trivyignore`, `VULNCHECK_IGNORE` in Makefile/Makefile.precommit, including `\` continuation lines) and `FilterSuppressed` drops those IDs before the model sees them. Previously the gate targets' echoed output (Makefile source, osv-scanner ignored-vuln listings) leaked operator-approved no-fix IDs into the captured table, so planning re-parked a task on a suppression the operator already approved even though the gate passed (observed 2026-08-24 on hue: all 8 suppressed IDs re-flagged at planning after the SC6 suppressions landed).

## v0.12.5

- fix: the ai_review step's no_new_tag check compares remote tags against only the update branch's own commits (git rev-list origin/master..HEAD) instead of the whole history reachable from the pinned filing ref — a legitimate release tag already on master is no longer misreported as a tag leaked from the update pipeline, while a tag on a branch-introduced commit still rejects the review
- fix: the ai_review step accepts an already-MERGED update PR as the shipped success state instead of rejecting it with "pr state is MERGED, expected OPEN" — a merged task routes to human_review for the operator to close rather than publishing status=failed and re-filing the task forever

## v0.12.4

- feat: the planning and execution phases resolve the task's clone ref to the repo's current default-branch HEAD at run start instead of the stale filing SHA — the go-version bump and its precommit gate always run against the repo's current tooling, the pinned SHA stays recorded for provenance and still names the deterministic work branch, and a current-HEAD resolution failure stops the run loudly rather than falling back to the stale base
- docs: update `docs/design.md` clone-at-ref semantics to the run-start-resolution behavior — § 3.3 annotates the frontmatter `ref` as filing-time provenance/dedupe only, § 4.3 planning/execution record resolve-at-run-start side effects and the resolution-failure escalation, and § 4.4 clarifies the `Result.branch` invariant uses the pinned filing SHA rather than the resolved HEAD
- fix: spec-filing dedup `branchExistsOnOrigin` no longer reads a checkout-stage "pathspec ... did not match any file(s)" error as "branch exists". `CloneAtRef` is a full clone + checkout, so a missing `build-fixer/<sha>` branch surfaces at checkout, not as the clone-stage "Remote branch ... not found" — the old classifier fell through to assume-exists and skipped the push, producing `already_filed` with no branch actually on origin. Found by the dev e2e (v0.12.3 ran planning → `file_spec` from real gh logs, then dedup-skipped the push). Regression test added.

## v0.12.3

- fix: build-fix planning prefers `gh run view --log-failed` over the task body's Failing Workflows table — the table carries run URLs + job names (metadata), not the error text, so the diagnosis escalated `needs_input` on real failures it had no log access to. The gh fetch now runs first when gh is available; a body that already carries a real `## Error` / `## Log` snippet still short-circuits it.
- fix: `extractFailingWorkflowLogEvidence` also matches the watcher's `## Error` log-section heading (the github-build watcher emits the log snippet under `## Error`, not `## Log`), so `include_logs`-enabled task bodies actually reach the diagnosis.

## v0.12.2

- exclude no-fix docker/containerd advisories in checker config (GO-2026-4883/4887/5064/5338/5622/5932 v1 no-fix)
## v0.12.1

- fix: ship the common-problems knowledge base in the runtime image — `docs/common-problems.md` lived at a path only the build stage copied (`COPY . /workspace`), so `/workspace/docs/common-problems.md` did not exist in the deployed pod and the guardrail reference was dead. Moved to `agent/docs/common-problems.md` (carried by `COPY agent/ /agent/`) and the guardrail now reads `/agent/docs/common-problems.md`. Found by live e2e: the v0.11.0 self-update job parked a no-fix-advisory task (`needs_input`) because the model never saw the KB.
- fix: align the KB's no-fix-advisory entry with the design-D4 parking gate — the planning step deterministically parks `action: "park"` findings for the operator (suppression is operator-gated by design), so the KB now instructs the model to classify accurately and name the suppression surfaces, not to auto-exclude. Autonomous exclusion of a no-fix advisory would bypass the operator's required review.

## v0.12.0

- feat: build-fix agent as a second domain task type (`task_type: build-fix`) in the `github-update-go-agent` binary, dispatched via the framework's `map[TaskType]*Agent` table. The fixer consumes build-failure tasks and resolves one of four verdicts in planning: build already green → `no_fix_needed` (close); stale dep/vuln → chain a `github-update-go` task via Kafka CreateCommand; code/test bug → file a `kind: bug` spec on `build-fixer/<sha-short>` (dedup via branch existence); ambiguous → escalate. Execution is pure Go (no LLM) — the spec is built deterministically from the diagnosis. No separate repo/deployment: shares the binary's core plumbing.
- fix: bot-review round 2 — mark the fix-step `ghToken` fields `display:"length"` (secret-field convention) and move the nil-producer guard inside `CreateBuildFixChainEmitter`'s returned closure so the factory body stays pure composition.
- fix: bot-review round 3 — add `display:"length"` to the `osExecGhCli.ghToken` field and add `ctx.Done()` checks in the two `BuildEnv` merge loops (`buildClaudeEnv` now takes ctx; `cmd/run-task` Run loop returns `ctx.Err()` on cancellation).
- fix: bot-review round 4 — add `display:"length"` to `AnthropicAuthToken` (main.go + cmd/run-task) and `PEMKeyFile`, and thread `ctx` through the fix-planning helpers (`readFixRequired`, `extractFailingWorkflowLogEvidence`, `parseFixPlan`, `writeSpecFile`) adding `ctx.Done()` cancellation checks in their loops.
- fix: bot-review round 5 — replace the unrecognized `display:"password"` tag with `display:"length"` on `AnthropicAuthToken` (the argument library only knows `hidden`/`length`, so `password` fell through and printed the token verbatim), remove the duplicate nil-producer guard from `CreateBuildFixChainEmitter` (the caller owns the nil decision), and add boundary logs to `FetchFailedLogs`' `gh run list`/`run view` calls.
- fix: bot-review round 6 — propagate ctx cancellation from `readFixRequired` (returns `ctx.Err()` instead of silently empty values), extract the chained-task assembly into `buildChainCommand` so `CreateBuildFixChainEmitter`'s closure stays pure composition (no conditional), and add boundary logs to `gh pr list` / `gh pr view`.
- fix: bot-review round 7 — move the clone-url conditional into `chainFrontmatter` so `buildChainCommand` is conditional-free, and return an empty (not nil) env map from `buildClaudeEnv` on ctx cancellation.
- fix: bot-review round 8 — extract the chain-producer close into a named `closeChainProducer` helper so the `CreateBuildFixChainEmitter` closure contains no conditional.

## v0.11.0

- feat: baked-in common-problems knowledge base — `docs/common-problems.md` encodes the recurring Go-update failure classes (golangci-lint/Go mismatch → `GOLANGCI_LINT_VERSION` bump; no-fix advisory → per-repo exclusion, never fixable ones; stale pinned task ref → re-checkout origin head; DeadlineExceeded → fail fast) with symptom → confirm → fix → do-not structure. `agent/.claude/CLAUDE.md` now instructs the agent to read it at planning and execution and apply fixes autonomously instead of parking for the operator.

## v0.10.2

- fix: trivy scanner-table parser reads the Installed version as the Fixed version. trivy's table output gained a Status cell between Severity and Installed Version, shifting the Fixed Version cell one position right; the parser still assumed the legacy six-column offset, so a finding was planned with `fixed_version` = its currently-installed version and the model's targeted `go get <pkg>@<installed>` was a no-op — the gate stayed red and CI failed on otherwise-approved PRs (golang.org/x/mod v0.37.0 → v0.40.0, GO-2026-6179/6180, CVE-2026-56864/56865). The parser now detects the Status cell and reads the correct cell in both layouts.

## v0.10.1

- fix: deterministic CHANGELOG hygiene in the execution step — Go now guarantees the CHANGELOG is bot-review-clean before commit: canonical preamble on fresh files (kills the `preamble-frozen` critical), `## Unreleased` after the preamble on existing files, and a `chore:` bullet naming the actual go.mod bumps (kills `conventional-prefix-required` and the silent no-release on bullet-less bumps). The model's bullet is best-effort and superseded by this step.

## v0.10.0

- feat: CI-pin preflight in planning — the agent detects a hardcoded `go-version:` in `.github/workflows/*.yml` and escalates to human review with a precise manual fix (replace with `go-version-file: go.mod`) instead of opening a PR doomed to fail CI. Matrix pins (deliberate multi-version testing) are correctly not flagged. Pure-Go detection, no LLM cost; the agent remains architecturally forbidden from editing workflows.

## v0.9.8

- fix: check `ctx.Done()` before each gate target in the gate re-run loop. Each target is a `make` subprocess that can run for minutes, so a cancelled context previously still walked the whole target list — which matters more now that the Job's remaining budget is what the salvage path spends.
- fix: salvage completed work when the Claude sub-call fails after the update already succeeded. The gate targets are the deterministic verdict and the model's self-report never was, so a failed sub-call now re-runs the gates instead of discarding the workdir: green gates commit, push, and open a pull request; red gates fail with the Claude error as the recorded cause. The salvaged pull request is forced to **draft** even on a `PR_TARGET=ready` deployment — green gates prove the code compiles and tests pass, but not that the model finished its non-gated duties (notably the CHANGELOG bullet, whose absence on an `autoRelease` repo means the change merges but never ships), so salvaged work always gets human review.
- fix: bound the execution Claude sub-call with a 15-minute timeout (`claudeExecutionTimeout`, mirroring the existing `bulkUpdateTimeout`). Previously the sub-call was bounded only by the Job's 1800s `activeDeadlineSeconds`, which the Go step does not survive — the deadline kills the pod, so the gate re-run, commit, and push after the call never execute and an already-green update is discarded. The timeout returns control to Go while Job budget remains.
- fix: classify Claude sub-call failures instead of hardcoding `error_category: unknown`, and add `permission_denied` to the (documented closed) `ErrorCategory` enum. Recording a refused tool call as `unknown` is what made the 2026-08-16 incident read as a deadline problem rather than an allowlist one.
- fix: allow read-only shell text utilities (`grep`, `head`, `tail`, `cat`, `wc`, `sort`, `uniq`) in both the planning and execution Claude tool scopes, and add a "Tool discipline" section to the execution prompt steering the model to the `Grep`/`Read` tools and telling it to treat a Bash refusal as final rather than retrying it. The execution allowlist omitted `grep` while the repair playbook itself instructed `go mod graph | grep <pkg>` — so the prompt told the model to run a command the allowlist denied. A denied Bash call was not a cheap failure: the model retried until the Job's 1800s `activeDeadlineSeconds` expired, discarding an update that had already reached `gate_exit: 0` (observed 2026-08-16 on `bborbe/ip`, where the work branch was never pushed and no PR was opened despite a green run). Every stage of a shell pipeline is permitted independently, so allowing `grep` alone would still have failed the observed `grep ... | head -20` command.

## v0.9.7

- chore: bump the image toolchain to Go 1.27.0 (Dockerfile `golang:` base) and golangci-lint to v2.12.2 (the pinned v2.11.4 cannot parse Go 1.27's export-data format — "export data version 4 is greater than maximum supported version 2" — once the toolchain is bumped). Planning's bump target is the toolchain baked into the image (design D5), so while the image carried `1.26.6`, every `update_scope: golang` task would have bumped repos from 1.26.6 → 1.26.6 — a no-op on the very thing the task exists to do. The frontmatter `latest_go: 1.26.7` on the emitted tasks was stale on top of that (go.dev announced 1.27.0 on 2026-08-19). Nothing checks that the image's Go is current when building — the image build passed `check-version-tag` (tag matches tree) while the toolchain inside lagged two releases. The repo's own go.mod directive stays at 1.26.6: golangci-lint v2.11.4 cannot yet parse Go 1.27's export-data format, and the directive is irrelevant to the sweep (target = runtime.Version() of the built binary, not the directive). This bump makes the 91 in-flight golang tasks target 1.27.0 once the image exists on docker.io; the deploy waits for the official `golang:1.27.0` image publish.

## v0.9.6

- fix: execution's `validatePlan` readiness check mirrors planning's scope-aware `hasWorkForScope` instead of trusting the model's `outcome`/`has_work` labels. v0.9.3 fixed PLANNING to route a `no_update_needed` + `has_work: false` plan to execution when the plan's own fields showed in-scope work — but execution's guard re-read `plan.HasWork` and rejected the same plan it had just been routed to. On 2026-08-19 `bborbe/log` (`update_scope: deps`) carried `outcome: no_update_needed` + `has_work: false` while `dep_updates_expected: true`; v0.9.3 sent it to execution, and this guard failed it again. Same root class as the planning `||`: a model verdict trusted where a computed field belongs. The plan in markdown is already scope-filtered by planning's `appliesScope`, so `hasWorkForScope` on it is consistent with the decision that routed there. Counterfactual-verified: old label check fails exactly the regression spec, fix passes.

## v0.9.5

- fix: a missing `AUTO_MERGE_LABEL` no longer costs the pull request. `gh pr create --label <name>` fails outright when the label does not exist in the repo, so with `AUTO_MERGE_LABEL: auto-merge` configured fleet-wide and no repo actually defining that label, every run died at PR creation with `could not add label: 'auto-merge' not found` — the 2026-08-19 deps sweep stopped producing PRs entirely while planning and execution both succeeded. `CreatePR` now retries once without the label, logs at V(0) naming the label so the operator can create it, and opens the PR anyway: losing the auto-merge opt-in beats losing the PR. Safe against duplicates because gh validates labels before creating anything — verified on `bborbe/backup`, which had no PR at all after the failure.

## v0.9.4

- fix: keep `npm` in the runtime image instead of `apk del`-ing it after it installs the Claude CLI. Node survived that delete, npm did not, so any repo whose `make precommit` recurses into a JS subproject died at the planning gate with `npm: No such file or directory` (exit 127) — a task that can never pass and therefore retries forever. Observed on `bborbe/backup` during the 2026-08 deps sweep: 4+ consecutive jobs, ~8 min each, identical failure, burning the serial slot at `maxConcurrentJobs: 1`. Costs 7.5 MB (`npm-11.11.0-r0`); node was already present for the Claude CLI. Audited the rest of that RUN block against the deployed v0.9.2 image — npm was the only binary installed-then-deleted; curl, bash, git, gh, make, jq, column, node, gcc, go, trivy and claude all resolve.
- docs: list `AUTO_MERGE_LABEL` in design.md § 5.1 Inputs and § 5.2 Outputs — it was documented in the README env table but missing from the design doc's input/output contract

## v0.9.3

- fix: planning's close decision is driven by the plan's structured fields, never by the model's `outcome` label alone. The label sat in front of the scope check as an `||`, so it could only ever widen the close — `hasWorkForScope` was able to remove work but never to rescue it. On 2026-08-19 `bborbe/argument` (`update_scope: deps`) came back `no_update_needed` + `has_work: false` while the SAME plan object carried `dep_updates_expected: true`, and its reason had the scope inverted ("dep updates are out of scope since update_scope is deps"). The task completed with no PR and, being completed, never retried — while the repo really had three direct dep updates waiting (`bborbe/errors`, `bborbe/time`, `onsi/ginkgo/v2`). The contradiction now logs at V(0) with repo, scope and the model's own reason. Being wrong in this direction is bounded: if the fields overstate the work, execution's no-effective-change guard writes `no_update_needed` and routes to done — a wasted pass beats a silent skip. The deps-scope prompt section now also states explicitly that dep updates are in scope and that `dep_updates_expected: true` forces `has_work: true`, but the code no longer depends on the model reading it correctly.

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
