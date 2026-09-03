---
status: completed
spec: [bug-gate-timeout-not-signaled]
summary: Added gateOutputIsTimeout classifier so the planning step's empty-on-error gate escalation names Go test-timeout hangs in the NeedsInput headline (generic message byte-for-byte unchanged for other failures), with unit test and CHANGELOG entry
execution_id: github-update-go-agent-gate-signal-exec-013-gate-timeout-signal
dark-factory-version: dev
created: "2026-09-03T21:35:00Z"
queued: "2026-09-03T19:30:40Z"
started: "2026-09-03T19:30:41Z"
completed: "2026-09-03T19:40:35Z"
---
<summary>
- When a repo gate target fails with no parseable scanner findings, the planning step escalates a single generic sentence (`gate target "X" failed (exit N) with no parseable findings — this gate is broken for repo R`) regardless of WHY it failed.
- A 600s Go test-timeout hang and a 2-second lint error produce byte-identical headlines — three real escalations (backup/npm, beactive/X11, lockbox/Go-1.27 test-helper hang) have already been misrouted by this.
- This change classifies the failure: when the captured gate output contains a Go test timeout signature, the escalation names the timeout (`test timed out`) while keeping the exit code, bounded output tail, and "next HEAD re-triggers" repair guidance.
- A failure without the timeout signature keeps the existing generic message — safe degradation, no wrong classification.
- The change is confined to one message-construction branch in `pkg/steps_planning.go` plus its tests.
</summary>

<objective>
In `pkg/steps_planning.go`, `runInspection`, when a gate target fails with no parseable findings (the `runErr != nil && len(rows) == 0` branch), detect a Go test timeout signature in the captured output and name it in the escalation headline. The generic message stays for every other failure mode. Nothing outside that branch + its tests changes.
</objective>

<context>
Read `/home/node/.claude/CLAUDE.md` and `/workspace/CLAUDE.md` (if present) for project conventions.

Read the current escalation branch in `/workspace/pkg/steps_planning.go` (around line 484):

```go
if runErr != nil && len(rows) == 0 {
    glog.V(2).Infof("planning: gate target %s failed exit=%d rows=0 output=%q", target, exitCode, output)
    return nil, nil, needsInput(fmt.Sprintf(
        "gate target %q failed (exit %d) with no parseable findings — this gate is "+
            "broken for repo %s: a re-run reproduces the identical result, so the agent "+
            "never retries it. Fix the target in the repo (or point it at tooling the "+
            "agent image provides) and push — the next HEAD re-triggers. Output tail:\n%s",
        target, exitCode, repo, truncateTail(output, gateTailMaxBytes),
    ))
}
```

The captured `output` for a hang ends with Go's test-timeout panic text, e.g. (real lockbox evidence):

```
panic: test timed out after 10m0s
...
FAIL    github.com/bborbe/lockbox    600.035s
make: *** [Makefile.precommit:35: test] Error 1
```

The signature to detect is case-insensitive `test timed out` — it is present in both `panic: test timed out after 10m0s` and `FAIL … 600.035s` (the `-v`/Ginkgo line). `gateTailMaxBytes` and `truncateTail` already exist in `pkg/gate_runner.go` — reuse them, do not duplicate.

Read `/workspace/pkg/steps_planning_test.go` — the `Describe("gate target failure (empty-on-error parks NeedsInput, never retried)", ...)` block at ~line 395 asserts the generic message shape (`gate target "check" failed \(exit [0-9-]+\) with no parseable findings`, plus `make: something broken`, `a re-run reproduces the identical result`, `Fix the target`, `next HEAD`). It must keep passing unchanged (the generic branch is untouched); the `setupFixture(fixtureMakefileBroken)` fixture produces non-timeout output.
</context>

<requirements>
1. **Add a timeout classifier.** In `pkg/steps_planning.go`, add a small deterministic helper (e.g. `gateOutputIsTimeout(output string) bool`) that returns true when the output matches the fixed signature: case-insensitive substring `test timed out`. Implement with `strings.Contains(strings.ToLower(output), "test timed out")` — no regexp needed, no configuration.

2. **Classify in the empty-on-error branch.** In `runInspection`, where the branch currently builds the escalation, branch on the classifier:
   - When the output carries the timeout signature, the headline names it — e.g. `gate target %q failed (exit %d) — test timed out with no parseable findings — this gate is broken for repo %s: …` — and keeps the exact same repair guidance sentences (`a re-run reproduces the identical result`, `Fix the target in the repo … the next HEAD re-triggers`) and the same `truncateTail(output, gateTailMaxBytes)` tail.
   - When it does not, the message is byte-for-byte the current generic form.
   The `NeedsInput` status and `glog.V(2)` logging remain unchanged in both cases.

3. **Add a unit test.** In `pkg/steps_planning_test.go`, extend (or add alongside) the empty-on-error Describe block with a case whose fixture output contains `panic: test timed out after 10m0s` (e.g. a `fixtureMakefileTimedOut` whose make output echoes that text, or `setupFixture` + a runner stub returning that output — follow whatever the existing fixture mechanism supports). Assert the resulting `NeedsInput` message: matches `test timed out`, still contains the exit code (`failed \(exit [0-9-]+\)`), still contains `Fix the target`, `next HEAD`, and the tail. Do NOT weaken or modify the existing generic-shape assertions.
</requirements>

<constraints>
- Confined to `pkg/steps_planning.go` (the empty-on-error branch + classifier helper) and `pkg/steps_planning_test.go`. Do NOT touch `pkg/gate_runner.go`, `parseScannerOutput`, the success path, or the parseable-findings path.
- Keep the generic message byte-for-byte unchanged for non-timeout failures.
- `NeedsInput` (never `Failed`) for the empty-on-error branch, as today.
- Do NOT add retry logic, backoff, max-attempts, or any config/flag — out of scope.
- Repo conventions: `github.com/bborbe/errors` wrapping (no `fmt.Errorf`), `glog.V(2)` logging, BSD header, `gofmt`/`goimports` clean.
- Container-autonomous: file edits + `make` only. No `kubectl`, no `docker`, no `gh`, no PR/deploy steps. Do NOT commit — dark-factory handles git.
</constraints>

<verification>
Run in `/workspace`:

1. **Targeted test:**
```
go test ./pkg/... -run Gate -count=1
# expect: ok — new timeout-classification test and existing empty-on-error generic-shape test both pass
```

2. **Full suite + gates** (both must exit 0):
```
make test
make precommit
```

3. **Filesystem confirmations** (no `git` in this container):
```
grep -n "test timed out" pkg/steps_planning.go          # expect: classifier helper + classified headline
grep -n "gateOutputIsTimeout" pkg/steps_planning.go     # expect: helper definition + call site
grep -n "panic: test timed out" pkg/steps_planning_test.go  # expect: the new fixture/assertion
```
</verification>
