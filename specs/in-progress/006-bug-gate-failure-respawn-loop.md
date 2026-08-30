---
status: prompted
approved: "2026-08-30T22:49:29Z"
generating: "2026-08-30T22:49:29Z"
prompted: "2026-08-30T22:56:12Z"
branch: dark-factory/bug-gate-failure-respawn-loop
---

## Summary

- When a repo's gate target fails and produces no parseable scanner findings, planning reports `failed`, which the controller routes to `phase: ai_review` — back inside the executor's spawn allowlist. The task respawns, hits the identical broken gate, and reports `failed` again, forever.
- Observed live on 2026-08-30: `bborbe/kafka-maxscale-cdc-connector` burned **26 Jobs in ~30 minutes** across two tasks, roughly one per minute, and only stopped when a human manually aborted them.
- A gate that fails without emitting a single parseable finding is broken for that repo, not flaky — re-running reproduces the identical exit code. That is the textbook `needs_input` case per the platform's own status taxonomy, not `failed`.
- Fix: this one branch returns `needs_input` instead of `failed`, with a message that tells the operator exactly what to repair. Every other planning failure class stays `failed`.
- Parking is free here: the watcher emits a fresh task per repo HEAD ref, so once the gate is fixed upstream the next HEAD produces a new task automatically — confirmed live, where refs `165e448` and `3ed7393` were emitted after the Makefile fix and the run against fixed master passed the gate and opened a PR.

## Problem

`github-update-go-agent`'s planning step runs each of the target repo's detected gate targets and parses their output into a findings table. When a target exits non-zero AND the parse yields zero rows, the step correctly refuses to read the empty output as "clean" — but it classifies the situation as `failed`. Per the platform's status taxonomy, `failed` means "infra failure, the next run might succeed", so the controller writes `phase: ai_review`, which is inside the executor's spawn allowlist (`{planning, in_progress, ai_review}`), and the task is spawned again. Because the cause is the repo's own broken Makefile target, the next run is bit-for-bit the same failure. The result is an unbounded respawn loop against a single-job-slot executor: one broken repo starves the whole fleet drain, and nothing in the current system stops it except a human noticing and aborting the tasks.

## Reproduction

Environment: `github-update-go-agent` on nuke-prod, 2026-08-30, target repo `bborbe/kafka-maxscale-cdc-connector`.

1. The repo carries a 2019-vintage Makefile `format` target that runs `go get golang.org/x/tools/cmd/goimports` (which no longer installs binaries) and then invokes `goimports` via `find -exec`.
2. `goimports` is not on `PATH` in the agent image, so the target fails:

   ```
   find: goimports: No such file or directory
   make: *** [Makefile:33: format] Error 1
   ```

3. `make precommit` therefore exits 2 and emits zero parseable advisory rows.
4. Planning reaches the `runErr != nil && len(rows) == 0` branch in `pkg/steps_planning.go` (~line 481) and returns `failed(...)`; the agent publishes `status=failed` and exits 0.
5. The controller routes `failed` → `status: in_progress`, `phase: ai_review`.
6. `ai_review` is in the executor's spawn allowlist, so the executor spawns the task again. Goto 2.
7. Observed effect: **26 Jobs in ~30 minutes**, ~one per minute, across tasks `7adcf106-d527-5140-8b61-6f57e304aafb` and `eda6d441-67e7-590a-8cc9-6ae4da1a4ce6`. A human aborted both tasks to stop it.
8. Every run additionally failed its pushgateway metrics push (DNS), so the loop produced no Prometheus signal — it was invisible until someone read the Job list.

No cap engaged. The executor's `trigger_count` / `max_triggers` cap is opt-in — `agent-task-executor/pkg/handler/task_event_handler.go:497` guards on `if _, ok := task.Frontmatter["max_triggers"]; ok && ...` — and the `github-update-go` watcher does not emit `max_triggers`. Nothing backstops this loop today.

## Expected vs Actual

**Expected**, per `agent/docs/task-flow-and-failure-semantics.md` § Status Taxonomy and § Result Routing (spec 010):

| Kind | Meaning | AgentStatus | Retry on next run? |
|---|---|---|---|
| Infra failure | next run might succeed | `failed` | Yes — controller writes `phase: ai_review`, executor re-spawns |
| Task-wrong | same input yields same answer | `needs_input` | No — controller clears `assignee`, leaves phase unchanged, executor does not re-spawn |

A repo whose gate target is broken is the second row: the same clone at the same ref runs the same recipe and produces the same non-zero exit. It must be handed to a human, once.

**Actual**: the branch returns `failed`, the task re-enters the allowlist, and the agent re-executes an experiment whose outcome is already known.

## Why this is a bug

- It contradicts the platform's own documented taxonomy: `failed` is defined as "next run might succeed"; here the next run provably cannot succeed.
- The consequence is unbounded, not merely wasteful — 26 Jobs in 30 minutes, halted only by human intervention, on an executor with a single job slot serving ~300 queued repos.
- The existing `## Run` doc comment in `pkg/steps_planning.go` (~line 120) documents the current routing (`a failing target with no parseable findings → Failed`), so the code and the taxonomy doc are in written disagreement.
- **This repo's own design doc already prescribes the fix.** `docs/design.md:227` (§ 7.3 Assumptions) states that the image ships only trivy plus go/make/git/gh/node, that scanners are expected to run via `go run tool@$(VERSION)` from the repo's Makefile, and that **"Violations → `needs_input`, not crash."** A Makefile target invoking a binary the image does not ship is exactly such a violation — so the intended routing was written down before the code chose `failed`.
- No cap backstops it: the trigger cap is opt-in and this watcher does not emit the field.

## Goal

A planning run against a repo whose gate target fails without producing a single parseable finding ends the task in the operator's inbox with a message naming what to fix, and is never automatically re-spawned. The classification of every other planning failure is unchanged: infra-shaped failures still retry, and an empty-on-error gate output is still never read as a clean gate.

## Non-goals

- Do NOT touch the executor's opt-in `trigger_count` / `max_triggers` cap, and do NOT add a `phase`+`ref`-keyed cap — that is tracked as separate work. A generic cap is a second, independent safety net; this spec fixes the misclassification that makes the net necessary in the first place.
- Do NOT fix the pushgateway DNS failure that made the loop invisible in Prometheus.
- Do NOT reclassify clone/auth failures, current-HEAD resolution failures, canceled-context aborts, task-marshal failures, Claude-runner or plan-parse failures, the fabricated / prefix-colliding vuln-ID rejection, or the malformed `.maintainer.yaml` fail-closed path. All remain `failed`.
- Do NOT change the "no gate target found in the Makefile" branch — it already returns `needs_input` and stays as-is.
- Do NOT add a retry counter, threshold, or opt-out flag to the agent for this branch — invariant; the routing is unconditional. If a future consumer needs a repo-specific override, that is a separate spec.
- Do NOT change the `needsInput` / `failed` helper shapes, and do NOT start writing `assignee`, `status`, or `## Failure` from the step — the controller owns the escalation envelope.

## Assumptions

- The `github-update-go` watcher emits a fresh update-go task per repo HEAD ref. A parked task on a stale ref therefore costs nothing: once the repo's gate is fixed upstream, the next HEAD produces a new task automatically. Confirmed live on 2026-08-30 — after the Makefile fix landed on master, new tasks were emitted for refs `165e448` and `3ed7393`, and the run against fixed master passed the gate and opened a PR.
- A parked task spawns no Jobs (`assignee: ""` removes it from the executor's match), so an indefinitely-parked task consumes no fleet capacity.
- Gate failure with zero parseable rows is deterministic for a given clone at a given ref — it is a property of the repo's Makefile plus the agent image, both fixed within a run.

## Acceptance Criteria

- [ ] A gate target that exits non-zero with zero parseable findings yields `AgentStatusNeedsInput` — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='gate target failure'` exits 0 and stdout contains `SUCCESS!` with `1 Passed` (the `-v` is required — `go test` suppresses passing-package output without it), and `grep -c 'AgentStatusNeedsInput' pkg/steps_planning_test.go` returns ≥1.
- [ ] The escalation message carries all four required elements — evidence: the focused test above asserts, on `result.Message`, that it (a) matches the regexp `gate target "check" failed \(exit [0-9-]+\)`, (b) contains `make: something broken` (the fixture's stderr, proving the output tail survived), (c) contains a substring stating a re-run reproduces the same result, and (d) contains a substring naming the operator action (fix the target and push; the next HEAD re-triggers). Additionally `grep -c 'truncateTail(output, gateTailMaxBytes)' pkg/steps_planning.go` returns `1` and `grep -c 'next HEAD' pkg/steps_planning.go` returns ≥1.
- [ ] The old `AgentStatusFailed` assertion for this branch is gone, and the negative assertion is inverted rather than deleted — evidence: `git diff -U0 pkg/steps_planning_test.go | grep '^[-+].*AgentStatus'` shows the removed `AgentStatusFailed` / `NotTo(Equal(...NeedsInput))` pair and the added `AgentStatusNeedsInput` / `NotTo(Equal(...Failed))` pair. The `Describe` text is updated in the same diff — evidence: `git diff -U0 pkg/steps_planning_test.go | grep -c '^[-+].*Describe("gate target'` returns `2`.
- [ ] The zero-rows conjunct is intact — a failing gate target that DOES emit parseable rows still contributes them and planning proceeds — evidence: `grep -c 'runErr != nil && len(rows) == 0' pkg/steps_planning.go` returns exactly `1`.
- [ ] Every failure class listed under Non-goals still yields `AgentStatusFailed` — evidence: `go test ./pkg/ -count=1` exits 0 with the pre-existing `Describe` blocks (`clone auth failure`, `current-HEAD resolution failure`, `fabricated plan ID rejection`, `prefix-collision plan ID rejection`, `unparseable claude output`, `environment-claim needs_input refutation`, `.maintainer.yaml consent gate`, `update_scope frontmatter`) unmodified — evidence: `git diff -U0 pkg/steps_planning_test.go | grep -cE 'Describe\("(clone auth failure|current-HEAD resolution failure|fabricated plan ID|prefix-collision|unparseable claude output|environment-claim|\.maintainer\.yaml consent|update_scope)' ; true` prints `0`, proving no sibling `Describe` block was touched.
- [ ] The `no gate target found` branch is unchanged — evidence: `git diff pkg/steps_planning.go | grep -c 'no gate target found' || true` prints `0`.
- [ ] The `Run` doc comment documents the new routing — evidence: `grep -c 'no parseable findings → Failed' pkg/steps_planning.go || true` prints `0`, and `grep -c 'no parseable findings → NeedsInput' pkg/steps_planning.go` returns `1`.
- [ ] `docs/design.md`'s planning `Failure` row enumerates the new routing, so the shipped behavior and the design doc agree — evidence: `grep -c 'no parseable findings → `needs_input`' docs/design.md` returns ≥1, on the `| Failure |` row of the planning-step table (currently line 138, which lists only `park`/nested-module/clone-auth/HEAD-resolution outcomes).
- [ ] `CHANGELOG.md` carries one bullet under `## Unreleased` describing the reclassification — evidence: `sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -ci 'needs_input'` returns ≥1. Note the `## Unreleased` header does **not** exist today (the file goes straight from the preamble to `## v0.17.3`), so the implementer must create it above `## v0.17.3`, not append to an existing section.
- [ ] `make precommit` exits 0 — evidence: exit code.

No new scenario. The behavior is a single-branch status classification in one Go function; a unit test with the existing `fixtureMakefileBroken` reaches it directly, and no deployment-only interaction is involved.

## Verification

### Container-executable

```
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='gate target failure'
go test ./pkg/ -count=1
grep -c 'runErr != nil && len(rows) == 0' pkg/steps_planning.go
grep -c 'no parseable findings → NeedsInput' pkg/steps_planning.go
sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md
make precommit
```

Expected: both `go test` invocations exit 0; both `grep -c` print `1`; the `sed` range contains a bullet naming `needs_input`; `make precommit` exits 0.

### Operator-executable (after merge + deploy) — NOT container-executable, does NOT gate verification

`kubectlnukeprod` does not exist in the verification container. Do not attempt this rung during spec verification; it is out-of-band operator confirmation recorded here for the deploy tail.

```
kubectlnukeprod -n prod get jobs -l task-type=github-update-go --sort-by=.metadata.creationTimestamp | tail -20
```

Expected on the next run against a repo with a broken gate: exactly one Job for that task, and the task file shows `assignee: ""` with the escalation message in its `## Result` block — no second Job for the same `task_identifier`.

## Desired Behavior

1. A gate target that exits non-zero and produces zero parseable findings ends the planning run with `needs_input`, so the controller clears `assignee`, leaves the phase unchanged, and the executor does not re-spawn the task.
2. The escalation message names the failing target and its exit code, states that the gate is broken for this repo so a re-run reproduces the identical result, tells the operator to fix the target in the repo (or point it at tooling the agent image provides) and push — the next HEAD re-triggers — and carries the truncated tail of the captured gate output.
3. A gate target that exits non-zero but produces at least one parseable finding is unaffected: its rows join the scanner table and planning proceeds to inspection (the normal "scanner found vulnerabilities" path).
4. Every other planning failure class continues to emit `failed`, so genuine infra flakes still get an automatic retry.
5. The `Run` doc comment describes this branch as routing to `NeedsInput`, so the code and `agent/docs/task-flow-and-failure-semantics.md` agree in writing.

## Constraints

- The empty-on-error invariant established by spec 002 must hold: zero parseable rows on a non-zero exit is never treated as a clean gate. Spec 002's Failure Modes row ("no park/`needs_input` is emitted on missing data") is superseded on the routing dimension only — the run still refuses to fabricate a clean gate or a plan, it just parks instead of retrying.
- Steps never mutate `assignee` or `status` and never write a `## Failure` section — the controller owns the escalation envelope. The `needsInput` and `failed` helpers in `pkg/steps_gh_token.go` keep their current shape (status + message only).
- The output tail stays bounded by `gateTailMaxBytes` (2000 bytes) via `truncateTail` — no new or larger output surface reaches the task page.
- The agent still exits 0 after publishing its result (Pattern B Job contract); this change alters the published status only.
- All existing tests in `pkg/` must pass unmodified except the single gate-failure `Describe` block.
- The `runErr != nil && len(rows) == 0` conjunction is load-bearing and must not be widened to `runErr != nil`.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery |
|---|---|---|---|
| Repo's gate target is broken (missing tool, dead recipe) — the fixed case | One run, `needs_input`, `assignee` cleared, no respawn | Task file shows `assignee: ""` and a `## Result` naming the target + exit code; exactly one Job for the `task_identifier` in `kubectlnukeprod -n prod get jobs` | Operator fixes the Makefile target in the repo and pushes; the watcher emits a new task for the next HEAD, which runs the fixed gate |
| Repo's gate stays broken indefinitely | Every task for that repo parks after one Job; no loop, no fleet capacity consumed | Recurring parked tasks for the same repo, one per HEAD | Fix the gate upstream, or set `goUpdate.autoUpdate: false` in the repo's `.maintainer.yaml` so planning skips it entirely |
| Transient infra makes the gate fail with zero rows (disk full, network blip during `go mod download`) | Misclassified as `needs_input`; the task parks instead of auto-retrying | Escalation message's output tail shows an infra-shaped error rather than a Makefile error | Operator re-delegates by setting `assignee` — one manual action. Accepted tradeoff: one re-delegate beats an unbounded loop, and the watcher emits a new task at the next HEAD regardless |
| Gate target exits non-zero but emits parseable findings | Unchanged — rows join the scanner table, planning proceeds to inspection | The `## Plan` block lists the findings | n/a (normal path) |
| Agent's Kafka result publish fails, or the Job is OOM-killed before publishing | Unchanged by this spec — the executor's Job informer synthesises a `failed` result (spec 009) and the task re-enters `ai_review` | Job in `Failed` terminal state with no published result | Existing behavior; bounded by the executor's own retry handling, out of scope here |
| Pushgateway DNS resolution fails during the run | Unchanged — metrics push fails, the run still publishes its status | Metrics gap in Prometheus | Out of scope. Impact is reduced regardless: the loop this masked no longer exists |

## Security / Abuse Cases

- The escalation message echoes output produced by a target repo's own Makefile, which is attacker-influenceable by anyone who can land a commit in that repo. This is not a new surface — the current `failed` message already echoes the same bytes — and it stays bounded by `truncateTail(output, gateTailMaxBytes)` at 2000 bytes.
- No new input is parsed, no new network call is made, no new file is written. The change is confined to which `AgentStatus` constant one branch returns and the human-readable text alongside it.
- Blast radius moves in the safe direction: a repo that could previously coerce the fleet into an unbounded Job loop by shipping a broken gate can now only park its own task.

## Do-Nothing Option

The loop recurs on every repo whose gate is broken for the agent image. The executor runs a single job slot against roughly 300 queued repos, so one such repo consumes the entire drain until a human notices — and the pushgateway failure means Prometheus shows nothing. The trigger cap that would otherwise bound it is opt-in and this watcher does not emit `max_triggers`, so the only current stop is manual task abort, as happened on 2026-08-30.

Alternatives considered:

1. **Make the watcher emit `max_triggers`** — bounds the loop at 3 Jobs but leaves the misclassification in place: the agent still tells the platform "retry might help" when it provably cannot, and the operator inbox message still lacks the repair instruction. Worth doing, separately, as defense in depth.
2. **Add a `phase`+`ref`-keyed cap in the executor** — a general safety net for every agent, tracked as separate work. Broader blast radius, needs its own design; does not remove the need for correct classification here.
3. **Retry N times inside the agent, then park** — the agent is a stateless per-Job process with no memory of prior runs; it would have to invent cross-run state that the platform already models via `trigger_count`.
