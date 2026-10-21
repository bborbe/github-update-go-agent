---
status: completed
summary: Execution sub-call now runs only build + targeted tests; a red deterministic gate gets exactly one bounded Claude repair sub-call before the gates re-run and decide the verdict.
execution_id: github-update-go-agent-gate-repair-exec-019-gate-fail-repair-subcall
dark-factory-version: v0.198.7
created: "2026-10-21T15:38:01Z"
queued: "2026-10-21T15:38:01Z"
started: "2026-10-21T15:39:07Z"
completed: "2026-10-21T15:45:18Z"
---

<summary>
- The execution Claude sub-call no longer runs the repo's gate targets (`make precommit` / `make check` / `make vulncheck`); it verifies its work with `go build ./...` plus `go test` on the packages it touched
- The deterministic gate re-run stays the only verdict on whether the work is green
- When that deterministic re-run finds a red target, the agent makes ONE bounded Claude repair attempt fed with the failing target and its output tail, then re-runs every gate target once more
- A second red result fails the task exactly as a red gate does today (same `## Result` shape, failing target, output tail)
- The salvage path (Claude sub-call itself failed) is unchanged and gets no repair attempt
- Motivation: on sun the exec sub-call hit its 15-minute ceiling in 3 of 4 Jobs, mostly running a cold full `make precommit` that the Go step then runs again
</summary>

<objective>
Remove the duplicated full-gate run from the execution sub-call without losing repair: the prompt restricts the model to build + targeted tests, and `executionStep` gains a single bounded gate-fail → Claude repair → gate re-run cycle so lint / vulncheck failures the model no longer sees can still be repaired.
</objective>

<context>
Read CLAUDE.md (if present) and README.md for project conventions.
Read `pkg/steps_execution.go` — `executionStep.Run`, `runUpdate`, `rerunGates`, `salvageAfterClaudeFailure`, `fail`, and the `claudeExecutionTimeout` constant with its comment (the style for a new timeout constant).
Read `pkg/prompts/execution.md` — step 7 "Green-gate", Repair playbook item 1, the "Command discipline" and "Output" sections all mention running gate targets.
Read `pkg/prompts/prompts.go` — the `//go:embed` + accessor pattern for prompt modules.
Read `pkg/gate_runner.go` — the `GateRunner` interface (`RunTarget` returns tail, exit code, err).
Read `pkg/steps_execution_test.go` — the `Describe("red gate after claude")` block shows how `gate.RunTargetReturnsOnCall` and `runner.RunReturns` drive the step; follow it for new tests.
Read `pkg/prompts/prompts_test.go` for how prompt modules are tested.
</context>

<requirements>
1. `pkg/prompts/execution.md`:
   - Replace step 7 "Green-gate" with a "Build + targeted test" step: run `go -C <workdir> build ./...`, then `go -C <workdir> test` on the packages whose directories were edited (a go.mod/go.sum-only change needs only the build), repair until both exit 0, and NEVER run the plan's gate targets (`make precommit`, `make check`, `make vulncheck`, `make test`) — state that the Go step runs every gate target afterwards and hands a red one back in a repair call.
   - Update the opening paragraph ("leave every gate target green"), Repair playbook item 1 ("Verify with the repo's `make test` target, never `go build ./...` alone"), the Command discipline line naming "the gate targets in step 7", the Output sentence "When every gate target is green", and the `blocked` bullet so none of them instruct the model to run gate targets.
2. Add `pkg/prompts/gate_repair.md` + `GateRepairPrompt()` accessor in `pkg/prompts/prompts.go` (same embed pattern). Content: the model is the gate-repair step; one named target failed (target + output tail are appended as context sections); make the minimal edits the output names; hard limits copied from execution.md's Scope section (no git/gh, workdir only, never `.github/workflows/`, never suppress a finding — no `//nolint`, ignore files, VULNCHECK_IGNORE, skipped tests — never revert the go directive or dependency bumps); same command discipline (foreground only, no background, no TaskOutput); may run the failing target at most once at the end; final message is one raw JSON object `{"notes": "...", "blocked": "<optional>"}`.
3. `pkg/steps_execution.go`:
   - Add a `gateRepairTimeout = 5 * time.Minute` constant with a comment in the `claudeExecutionTimeout` style (why bounded: Job budget must remain for the second gate run + commit/push).
   - On the normal path in `Run` (Claude sub-call succeeded), replace the direct `rerunGates` call with a sequence: run the gates; if a target is red, invoke ONE Claude sub-call via `s.runner.Run` with `prompts.GateRepairPrompt()` + `## Workdir`, `## Failing gate target`, `## Gate output` sections, under a `context.WithTimeout(ctx, gateRepairTimeout)`; then run every gate target again. Red on the second run → fail exactly as `rerunGates` does today (same `ResultOutput.FailedTarget` / `GateExit`, message containing `gate target %q failed`). A repair sub-call error or unparseable reply is logged with `glog.Warningf` and does not short-circuit — the second gate run is still the verdict. If `ctx` has a deadline and less than `gateRepairTimeout` plus 5 minutes remains, skip the repair, log `glog.Warningf("execution: skipping gate repair — insufficient job budget")`, and fail as `rerunGates` does today; if `ctx` carries no deadline, run the repair. Add a test for the skip.
   - Refactor so the "find first red target" logic is shared rather than duplicated (e.g. a helper returning target, tail, exit code, err that `rerunGates` and the new sequence both use). Keep the `ctx.Done()` check between targets.
   - Log the repair path so it is greppable in Job logs: `glog.Infof("execution: gate %q red — running repair sub-call", target)` before the call and a V(2) line with the outcome after.
   - `salvageAfterClaudeFailure` keeps calling the existing no-repair path unchanged.
   - Update the `Run` doc comment step 6 to describe the repair cycle.
4. Tests in `pkg/steps_execution_test.go` (Ginkgo/Gomega, existing mocks):
   - red → repair → green: first gate run red on one target, second run all green → PR created, `runner.RunCallCount()` == 2 (execution + repair), repair prompt contains the failing target name and the output tail.
   - red → repair → red: fails with `gate target "check" failed`, no commit/push/PR, `## Result` FailedTarget set, `runner.RunCallCount()` == 2.
   - repair sub-call returns an error, second gate run green → PR still created.
   - green first time → `runner.RunCallCount()` == 1 (no repair call).
   - Adjust the existing `Describe("red gate after claude")` expectations to the new call sequence.
   - Salvage path: claude execution error + red gates → no repair call (`runner.RunCallCount()` == 1).
5. `pkg/prompts/prompts_test.go`: assert `ExecutionPrompt()` no longer contains `run EVERY gate target` and does contain `go -C <workdir> build ./...`; assert `GateRepairPrompt()` is non-empty and forbids `//nolint`.
6. Update `docs/design.md` wherever it describes the execution sub-call running the gate targets, to describe build + targeted test plus the bounded repair cycle.
7. `CHANGELOG.md` may have no `## Unreleased` section — if absent, create `## Unreleased` directly above the newest `## vX.Y.Z` heading (never edit a released section) and add the bullet: `- feat: execution sub-call runs build + targeted tests only; a red deterministic gate gets one bounded Claude repair attempt before failing`.
8. Self-check: re-run the verification below and walk every summary bullet against the change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- The deterministic gate run remains the only verdict; the model's report never decides pass/fail
- Exactly one repair attempt per Job — no loop, no retry on repair failure
- Do not change `claudeExecutionTimeout`, the salvage path's draft-forcing, the bulk update, or any git/PR side effect
- Errors via `github.com/bborbe/errors` (`errors.Wrap` / `errors.Errorf` with ctx), never `fmt.Errorf`
- Never touch `.github/workflows/`
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
Run `! grep -q 'run EVERY gate target' pkg/prompts/execution.md` -- must succeed.
Run `grep -c 'GateRepairPrompt' pkg/steps_execution.go` -- must print a number >= 1.
</verification>
