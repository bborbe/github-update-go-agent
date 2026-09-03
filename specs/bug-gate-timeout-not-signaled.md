---
status: draft
kind: bug
created: "2026-09-03T21:30:00Z"
---

## Summary

- When a repo gate target (`make precommit` / `check` / `vulncheck`) fails with no parseable scanner findings, the agent escalates `gate target "X" failed (exit N) with no parseable findings — this gate is broken for repo R` — the same one-line message regardless of WHY it failed.
- That single sentence has now hidden three different root causes across three repos (missing npm, missing X11 headers, a Go-1.27 test-helper hang that busy-loops for the full 600s test timeout) — the hang and a genuine lint error are indistinguishable to the operator.
- The underlying evidence is already captured (the escalation appends a bounded output tail), but the headline misleads: a **timeout/hang** reads as "the gate is broken" instead of "the test hung".
- This change classifies the failure: when the output carries a Go test timeout signature, the escalation names the timeout explicitly (`test timed out after 600s`) while keeping the exit code, output tail, and "next HEAD re-triggers" repair guidance.

## Problem

`steps_planning.go` `runInspection` parks `NeedsInput` with `gate target %q failed (exit %d) with no parseable findings — this gate is broken for repo %s …` whenever a gate exits non-zero with zero parsed rows. A 600s test-timeout hang and a 2-second lint failure produce byte-identical headlines. Operators triaging escalations cannot tell "the repo's gate is misconfigured" from "the repo's tests hang under this toolchain", so three consecutive escalations (backup/npm, beactive/X11, lockbox/Go-1.27 test-helper) each required manual reproduction to discover the real cause. The lockbox case (2026-09-01) is the third instance of the same shape — the durable fix is to make the gate report the signal it already has.

## Goal

A gate failure with no parseable findings reports its underlying signal. A timeout/hang is named as a timeout (with duration when visible), a fast error-exit keeps the generic form — and both keep the exit code, the bounded output tail, and the existing "fix the target / next HEAD re-triggers" repair guidance. An operator reading the escalation can classify the failure without re-running the repo.

## Non-goals

- Do NOT change the parseable-findings path, the success path, or the gate runner itself (`RunTarget` / `RunTargetFull`).
- Do NOT add retry logic, backoff, or max-attempts — that is separate work (the respawn-loop task family).
- Do NOT change how findings rows are parsed or suppressed.
- Do NOT attempt to classify arbitrary failure modes beyond the timeout signature — everything else keeps the current generic form.

## Reproduction

1. Run a repo whose `make test` hangs past Go's default 10-minute test timeout, e.g. `bborbe/lockbox` at `dbbb1be` under Go 1.27 (test helper violates the `io.Reader` contract; the rewritten `jsontext` decoder busy-loops). The gate output ends with:
   ```
   panic: test timed out after 10m0s
   ...
   FAIL    github.com/bborbe/lockbox    600.035s
   make: *** [Makefile.precommit:35: test] Error 1
   ```
2. The planning step emits:
   ```
   gate target "precommit" failed (exit 2) with no parseable findings — this gate is broken for repo bborbe/lockbox: …
   ```
   The headline ("this gate is broken") points the operator at the repo's gate/tooling, not at the hang that is visible in the tail. Observed on the live escalation `Update Go bborbe-lockbox dbbb1be` (2026-08-27 job, `exit 2, no parseable findings`).

## Expected vs Actual

**Expected:** the escalation names the underlying signal — e.g. `gate target "precommit" failed (exit 2) — test timed out (600s) with no parseable findings — …` — so a hang is distinguishable from a fast gate error without re-running the repo.

**Actual:** every empty-on-error gate failure emits the identical `with no parseable findings — this gate is broken` sentence, regardless of timeout vs fast error-exit. (The exit code and a bounded output tail are present, but the headline is the operator's first and only classification cue.)

## Why this is a bug

The gate already captures the evidence (output tail with `panic: test timed out after 10m0s`, `FAIL … 600.035s`, `make: *** Error 1`) but discards it as a classification signal: the message construction in `steps_planning.go` (`runInspection`, the `runErr != nil && len(rows) == 0` branch) never inspects the output for what kind of failure occurred. A hang and a fast failure are operationally opposite (repair vs abandon; the one burns 600s per retry) yet the operator gets no signal telling them apart. Three real escalations across three repos have already been misrouted by this one sentence.

## Acceptance Criteria

- [ ] A gate failure whose output contains a Go test timeout signature (e.g. `panic: test timed out after 10m0s`, `FAIL <pkg> <dur>s`, `test timed out`) produces an escalation that names the timeout — message matches `test timed out` and keeps the exit code, the bounded output tail, and the "next HEAD re-triggers" guidance. — evidence: unit test with a fixture carrying the timeout text; assertion on the resulting `NeedsInput` message.
- [ ] A gate failure with no timeout signature keeps the existing generic escalation shape (`gate target "X" failed (exit N) with no parseable findings — this gate is broken …`). — evidence: existing test for the empty-on-error branch still passes.
- [ ] Both message shapes still include the exit code and the bounded output tail (`…[truncated]…` allowed at 2000 bytes) and park `NeedsInput` (never `Failed`). — evidence: test assertions on message substrings and `AgentStatusNeedsInput`.
- [ ] `make precommit` is green on the fix branch. — evidence: green precommit run.

## Verification

```bash
# From repo root, on the fix branch:
go test ./pkg/... -run Gate 2>&1 | tail -3          # expect: new + existing gate tests pass
make test                                           # expect: ok
make precommit                                      # expect: green
```

## Desired Behavior

1. When `runInspection` hits the empty-on-error branch and the gate output contains a Go test timeout signature, the escalation message names it — e.g. `gate target "precommit" failed (exit 2) — test timed out with no parseable findings — …` — and retains the existing repair guidance ("Fix the target in the repo … the next HEAD re-triggers") and the bounded output tail.
2. When the same branch sees output without a timeout signature, the message is unchanged from today (generic `no parseable findings — this gate is broken …`).
3. The classification is deterministic: a simple signature match on the captured output (e.g. case-insensitive `test timed out` / `panic: test timed out after`), no LLM, no heuristics beyond the fixed signature.
4. Exit code, `NeedsInput` status, and bounded tail behavior are unchanged in both branches.
5. The parseable-findings and success paths are untouched.

## Constraints

- The change is confined to the escalation-message construction in `pkg/steps_planning.go` (`runInspection`, the `runErr != nil && len(rows) == 0` branch) plus tests in `pkg/steps_planning_test.go`. No change to `pkg/gate_runner.go`, `parseScannerOutput`, or any other file.
- Keep the existing repair-guidance sentences and the `truncateTail(output, gateTailMaxBytes)` tail in both message shapes.
- `NeedsInput` (never `Failed`) for the empty-on-error branch, as today.
- Repo conventions: `errors` wrapping (no `fmt.Errorf`), `glog.V(2)` logging, BSD header, `gofmt`/`goimports` clean.
- This repo's own `make precommit` must stay green under the host toolchain.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Timeout signature regexp too narrow (misses a variant) | Falls back to the generic message — safe degradation, no wrong classification | Broaden the signature in a follow-up |
| Timeout signature too broad (matches a lint line containing "timed out") | A fast failure mislabeled as a timeout | Keep the signature anchored to Go-test timeout text (`panic: test timed out after`, `FAIL … test timed out`) |
| Existing tests break on the new message shape | CI red | Update the affected assertions to the new classified shape; keep the generic-shape test for the no-signature branch |
