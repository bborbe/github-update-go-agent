---
status: completed
spec: [006-bug-gate-failure-respawn-loop]
summary: 'Reclassified the empty-on-error gate target failure branch in runInspection from failed to needsInput (parking the broken gate instead of respawning), updated the Run doc comment, inverted and expanded the gate-failure unit test, added the failing-gate-with-rows fixture and locking Describe block, recorded the routing in docs/design.md, and created the CHANGELOG ## Unreleased entry — all verification green including make precommit exit 0.'
execution_id: github-update-go-agent-needsinput-exec-012-spec-006-gate-failure-respawn-loop
dark-factory-version: dev
created: "2026-08-30T22:51:31Z"
queued: "2026-08-30T23:00:48Z"
started: "2026-08-30T23:00:49Z"
completed: "2026-08-30T23:05:35Z"
branch: dark-factory/bug-gate-failure-respawn-loop
---

<!-- NOTE FOR THE AUDITOR (not part of the executable task):
Single-prompt decomposition: this is one branch-level status-classification
change in one function (pkg/steps_planning.go runInspection) plus its unit
test and two small doc edits. The code change, its test, and the doc-comment
edit in the same file cannot be verified independently, so they ship together;
docs/design.md and CHANGELOG.md are 1-2 line edits belonging to the same
change. Splitting would produce a second prompt with no independently
verifiable postcondition.

No scenario prompt is emitted: the scenario-writing four-condition test fails
on condition (a) — the existing `fixtureMakefileBroken` reaches the
`runErr != nil && len(rows) == 0` branch directly through a unit test, and the
spec's "No new scenario" paragraph is explicit. The operator-side verification
rung (kubectlnukeprod) stays on the spec's Verification ladder, not here.

The test addition "failing gate that emits parseable findings contributes
rows" (new Describe block) locks the load-bearing zero-rows conjunction from
widening to `runErr != nil` (spec Desired Behavior 3). Its text deliberately
avoids the substring "gate target failure" so the spec's focused-ginkgo
evidence (`-ginkgo.focus='gate target failure'` → exactly `1 Passed`) still
holds.
-->

# Gate target failure with zero findings parks NeedsInput instead of respawning

<summary>
- A planning run whose gate target exits non-zero with zero parseable findings now ends the task as `needs_input` instead of `failed`.
- The escalation message names the failing target and its exit code, states that the gate is broken for this repo so a re-run reproduces the identical result, tells the operator to fix the target and push (the next HEAD re-triggers), and carries the truncated gate-output tail.
- Because `needs_input` clears the assignee and leaves the phase unchanged, the executor no longer re-spawns the identical broken run — the unbounded Job respawn loop (26 Jobs / 30 min on 2026-08-30) is broken at its root cause.
- Every other planning failure class is untouched and still returns `failed` (clone/auth, current-HEAD resolution, canceled context, marshal, claude-runner, plan-parse, fabricated/prefix-colliding vuln ID, `.maintainer.yaml` fail-closed).
- A failing gate that DOES emit parseable findings is unaffected: its findings still feed the plan and planning proceeds — now locked by a dedicated regression test.
- The "no gate target found" branch is unchanged (already `needs_input`).
- The step's own documentation now describes this branch as parking rather than failing, bringing the code into written agreement with the platform's status taxonomy.
- The design document and the changelog record the reclassification.
</summary>

<objective>
Reclassify the one planning branch where a gate target exits non-zero with zero parseable findings from `failed` (retryable) to `needs_input` (task-wrong, park), with an escalation message naming the broken target, the exit code, the deterministic-re-run fact, and the operator repair action — so a repo with a broken gate parks its own task instead of respawning forever, while every other failure class keeps its current classification.
</objective>

<context>
Read the injected container `CLAUDE.md` (`/home/node/.claude/CLAUDE.md`) for project conventions — there is no repo-root CLAUDE.md.

Read the spec fully: `specs/in-progress/006-bug-gate-failure-respawn-loop.md` — Goal, Desired Behaviors 1-5, Constraints, Failure Modes rows 1-4, Acceptance Criteria, and the "No new scenario" paragraph.

Read fully before changing anything:
- `pkg/steps_planning.go` — the whole file. The branch to change is inside `runInspection` (around line 478-488): the `runErr != nil && len(rows) == 0` guard that currently returns `failed(...)`. Note `repo` is already in scope at the top of `runInspection` (`repo, _ := md.Frontmatter.String("repo")`, line 465). The `## Run` doc comment is around line 118-120 (item 3: `a failing target with no parseable findings → Failed`). `fmt` and `glog` are already imported.
- `pkg/steps_gh_token.go` — the `needsInput` / `failed` helper shapes (lines 143-160): both take a single `msg string` and return `*agentlib.Result` with `Status` + `Message` only. These shapes are UNCHANGED.
- `pkg/gate_runner.go` — `gateTailMaxBytes` (2000), `truncateTail(s string, maxBytes int)`, and `GateRunner.RunTargetFull` (the full raw output the branch already carries through `truncateTail(output, gateTailMaxBytes)`).
- `pkg/steps_planning_test.go` — the whole file (Ginkgo/Gomega, external `package pkg_test`). The single `Describe("gate target failure (empty-on-error is not clean)", ...)` block (lines 391-406), the `fixtureMakefileBroken` fixture (lines 67-71), the `setupFixture` helper (lines 88-95), the outer `BeforeEach` (lines 114-138), and the imports (`agentlib "github.com/bborbe/agent"`, `stderrors "errors"`, `pkg "github.com/bborbe/github-update-go-agent/pkg"`). The row format `GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6` is the recognized osv shape (see `fixtureMakefile` line 44 and `parseScannerOutput` in `pkg/scanner_table.go`).
- `docs/design.md` — the planning-step `| Failure |` row (line 138) is the one cell this prompt edits; § 7.3 (line 227) already prescribes `Violations → needs_input, not crash` and needs no change.
- `CHANGELOG.md` — the preamble (lines 1-7) ends with no `## Unreleased` section; the first released header is `## v0.17.3` (line 8). `## Unreleased` must be CREATED above `## v0.17.3`, not appended.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage ≥80%.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and entry style (prefix required; `fix:` → patch).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — GoDoc comment style for the `Run` doc-comment edit.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits (`funlen` 80 lines / 50 statements, `golines` 100) — `make format` runs golines in-place, so keep message literals under 100 cols.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.

Run this first to confirm the exact current shapes (all must match the shapes quoted in `<requirements>`):

```bash
grep -n 'runErr != nil && len(rows) == 0\|no parseable findings\|needsInput(fmt\|failed(fmt' pkg/steps_planning.go
grep -n 'gate target failure\|fixtureMakefileBroken\|AgentStatus' pkg/steps_planning_test.go
grep -n 'Failure |' docs/design.md
grep -n '^## v0.17.3' CHANGELOG.md
grep -n 'func needsInput\|func failed' pkg/steps_gh_token.go
```
</context>

<requirements>
1. **Reclassify the empty-on-error gate failure branch from `failed` to `needsInput`.** In `pkg/steps_planning.go`, inside `runInspection`, replace this exact block:

   ```go
   		output, exitCode, runErr := s.gate.RunTargetFull(ctx, workdir, target)
   		rows := parseScannerOutput(target, output)
   		if runErr != nil && len(rows) == 0 {
   			glog.V(2).
   				Infof("planning: gate target %s failed exit=%d rows=0 output=%q", target, exitCode, output)
   			return nil, nil, failed(fmt.Sprintf(
   				"gate target %q failed (exit %d) with no parseable findings: %s",
   				target, exitCode, truncateTail(output, gateTailMaxBytes),
   			))
   		}
   		table = append(table, rows...)
   ```

   with:

   ```go
   		output, exitCode, runErr := s.gate.RunTargetFull(ctx, workdir, target)
   		rows := parseScannerOutput(target, output)
   		if runErr != nil && len(rows) == 0 {
   			glog.V(2).
   				Infof("planning: gate target %s failed exit=%d rows=0 output=%q", target, exitCode, output)
   			return nil, nil, needsInput(fmt.Sprintf(
   				"gate target %q failed (exit %d) with no parseable findings — this gate is "+
   					"broken for repo %s: a re-run reproduces the identical result, so the agent "+
   					"never retries it. Fix the target in the repo (or point it at tooling the "+
   					"agent image provides) and push — the next HEAD re-triggers. Output tail:\n%s",
   				target, exitCode, repo, truncateTail(output, gateTailMaxBytes),
   			))
   		}
   		table = append(table, rows...)
   ```

   The message MUST contain, verbatim-shaped: the `gate target %q failed (exit %d)` prefix (matches the acceptance regexp `gate target "check" failed \(exit [0-9-]+\)`), the substring `a re-run reproduces the identical result`, the substring `next HEAD`, the operator action `Fix the target`, and the output tail via `truncateTail(output, gateTailMaxBytes)` (unchanged, single use). `repo` is the variable already declared at the top of `runInspection`. Keep the `glog.V(2)` line byte-identical. Do NOT change the `if` condition — the `runErr != nil && len(rows) == 0` conjunction is load-bearing (spec Constraints: never widen to `runErr != nil`). Do NOT touch `table = append(table, rows...)` or any other line in the function.

2. **Update the `Run` doc comment (item 3).** In `pkg/steps_planning.go`, the comment block at lines 118-120 currently reads:

   ```go
   //  3. Detect gate targets from the Makefile in Go, run each via the
   //     GateRunner, and capture the full raw scanner output (no gate target →
   //     NeedsInput; a failing target with no parseable findings → Failed).
   ```

   Change only `→ Failed` to `→ NeedsInput` in the final clause, so item 3 ends `a failing target with no parseable findings → NeedsInput`. Do not change items 1, 2, 4, 5, 6, 7 (clone/auth, resolution, fabricated/prefix-colliding ID, park, no_update_needed, ready all keep their documented routing).

3. **Rewrite the single gate-failure unit test — invert the classification assertions and assert all four escalation-message elements.** In `pkg/steps_planning_test.go`, replace the entire block (lines 391-406):

   ```go
   	Describe("gate target failure (empty-on-error is not clean)", func() {
   		BeforeEach(func() {
   			setupFixture(fixtureMakefileBroken)
   		})

   		It("fails naming the target and exit code, never reads empty as clean", func() {
   			result, err := step.Run(ctx, md)
   			Expect(err).To(BeNil())
   			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
   			Expect(
   				result.Message,
   			).To(MatchRegexp(`gate target "check" failed \(exit [0-9-]+\) with no parseable findings`))
   			Expect(result.Message).To(ContainSubstring("make: something broken"))
   			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusNeedsInput))
   		})
   	})
   ```

   with:

   ```go
   	Describe("gate target failure (empty-on-error parks NeedsInput, never retried)", func() {
   		BeforeEach(func() {
   			setupFixture(fixtureMakefileBroken)
   		})

   		It("parks NeedsInput naming target, exit code, and repair action", func() {
   			result, err := step.Run(ctx, md)
   			Expect(err).To(BeNil())
   			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
   			Expect(
   				result.Message,
   			).To(MatchRegexp(`gate target "check" failed \(exit [0-9-]+\)`))
   			Expect(result.Message).To(ContainSubstring("make: something broken"))
   			Expect(result.Message).To(ContainSubstring("a re-run reproduces the identical result"))
   			Expect(result.Message).To(ContainSubstring("Fix the target"))
   			Expect(result.Message).To(ContainSubstring("next HEAD"))
   			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusFailed))
   		})
   	})
   ```

   The old `AgentStatusFailed` / `NotTo(Equal(...NeedsInput))` assertions are REMOVED and replaced by `AgentStatusNeedsInput` / `NotTo(Equal(...Failed))` (spec Acceptance Criteria: the negative assertion is inverted, not deleted). The new Describe text MUST contain `gate target failure` (the focused-ginkgo evidence depends on it) and must keep exactly ONE `It` (the focused run must report exactly `1 Passed`).

4. **Add a fixture for the failing-gate-with-rows case.** In `pkg/steps_planning_test.go`, next to `fixtureMakefileBroken` (after line 71), add:

   ```go
   // fixtureMakefileBrokenWithFindings defines a gate target that exits non-zero
   // while still emitting a parseable advisory row — only the zero-rows-on-error
   // case parks (spec 006 Desired Behavior 3: rows still join the scanner table).
   var fixtureMakefileBrokenWithFindings = ".PHONY: check\n" +
   	"check:\n" +
   	"\t@echo 'GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6'; exit 1\n"
   ```

5. **Add a new `Describe` locking the zero-rows conjunct (Desired Behavior 3).** In `pkg/steps_planning_test.go`, immediately after the rewritten gate-failure `Describe` block from requirement 3, add:

   ```go
   	Describe("failing gate that emits parseable findings contributes rows", func() {
   		BeforeEach(func() {
   			setupFixture(fixtureMakefileBrokenWithFindings)
   			runner.RunReturns(nil, stderrors.New("stop here"))
   		})

   		It("proceeds past the empty-on-error branch and reaches the inspection call", func() {
   			result, err := step.Run(ctx, md)
   			Expect(err).To(BeNil())
   			Expect(runner.RunCallCount()).To(Equal(1))
   			// Failed comes from the stub runner error, not the gate branch.
   			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
   		})
   	})
   ```

   This proves a failing gate that emits a parseable row is NOT parked by the branch (had the guard been widened to `runErr != nil`, this fixture would return `needs_input` and the runner would never be called). `stderrors` is already imported in this file. IMPORTANT: the Describe text and It text MUST NOT contain the substring `gate target failure` — the spec's focused-ginkgo evidence (`-ginkgo.focus='gate target failure'` must report exactly `1 Passed`) must not match this block.

6. **Design doc — record the new routing.** In `docs/design.md`, edit the planning-step `| Failure |` row (line 138). Current:

   ```
   | Failure | any `park`-action vuln → `needs_input` naming CVEs (D4); nested/multi-module root → `needs_input`; clone/auth fail → `failed`; current-HEAD resolution fail → `failed` naming the resolution step (no fallback to the stale pinned ref) |
   ```

   Insert the gate-failure routing after the nested-module clause, leaving everything else byte-identical:

   ```
   | Failure | any `park`-action vuln → `needs_input` naming CVEs (D4); nested/multi-module root → `needs_input`; gate target fails with no parseable findings → `needs_input` (broken gate parks, never auto-retried — spec 006); clone/auth fail → `failed`; current-HEAD resolution fail → `failed` naming the resolution step (no fallback to the stale pinned ref) |
   ```

   Do NOT edit any other design.md passage (the § 7.3 "Violations → needs_input, not crash" assumption is already consistent).

7. **CHANGELOG — create `## Unreleased`.** In `CHANGELOG.md`, the preamble currently runs straight into `## v0.17.3` (line 8). Insert, immediately above `## v0.17.3` (do NOT append to an existing section):

   ```markdown
   ## Unreleased

   - fix: a planning gate target that exits non-zero with zero parseable findings now parks the task with `needs_input` naming the broken target and exit code instead of publishing `failed` — the controller clears assignee and the executor no longer re-spawns the identical broken run, ending the unbounded Job respawn loop on repos with broken Makefile targets (observed on bborbe/kafka-maxscale-cdc-connector 2026-08-30: 26 Jobs in ~30 minutes)
   ```
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The execution-step gate re-run (`pkg/steps_execution.go:480`) and the review-step gate check (`pkg/steps_review.go:234`) are OUT OF SCOPE and must not be touched — a red gate there still returns `failed`.
- The empty-on-error invariant established by spec 002 must hold: zero parseable rows on a non-zero exit is never treated as a clean gate. The `runErr != nil && len(rows) == 0` conjunction is load-bearing and must NOT be widened to `runErr != nil`. The run still refuses to fabricate a clean gate or a plan — it just parks instead of retrying.
- Do NOT change the `needsInput` / `failed` helper shapes in `pkg/steps_gh_token.go` (status + message only), and do NOT start writing `assignee`, `status`, or `## Failure` from the step — the controller owns the escalation envelope. This change alters the published status only; the agent still exits 0 after publishing (Pattern B Job contract).
- Do NOT reclassify any other planning failure class: clone/auth failures, current-HEAD resolution failures, canceled-context aborts, task-marshal failures, claude-runner or plan-parse failures, the fabricated/prefix-colliding vuln-ID rejection, and the malformed `.maintainer.yaml` fail-closed path all remain `failed` (spec Non-goals).
- Do NOT change the "no gate target found in the Makefile" branch — it already returns `needs_input` and stays as-is.
- Do NOT add a retry counter, threshold, or opt-out flag for this branch — the routing is unconditional (spec Non-goals).
- Do NOT touch the executor's opt-in `trigger_count` / `max_triggers` cap and do NOT add a `phase`+`ref`-keyed cap (spec Non-goals, separate work). Do NOT fix the pushgateway DNS failure (spec Non-goals).
- The output tail stays bounded by `gateTailMaxBytes` (2000 bytes) via `truncateTail` — no new or larger output surface reaches the task page.
- All existing tests in `pkg/` must pass unmodified except the single gate-failure `Describe` block (rewritten in requirement 3) and the one new `Describe` block (requirement 5). Do NOT touch the pre-existing `Describe` blocks (`clone auth failure`, `current-HEAD resolution failure`, `fabricated plan ID rejection`, `prefix-collision plan ID rejection`, `unparseable claude output`, `environment-claim needs_input refutation`, `.maintainer.yaml consent gate`, `update_scope frontmatter`, `no gate target`, `park path`, etc.).
- No new module dependencies; do NOT run `go mod vendor` and do NOT use `-mod=vendor` (the repo does not commit `vendor/`; use `-mod=mod`).
- Keep functions inside the `funlen` limit (80 lines / 50 statements) and lines under 100 chars (`golines` — `make format` runs golines in-place, so the message literals in requirement 1 must already be under 100 cols).
- Existing tests must still pass; new/changed code needs ≥80% statement coverage.
</constraints>

<verification>
Run `make test` after each change (fast feedback), then run the full suite and the focused evidence test:

```bash
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='gate target failure'
# expect: exit 0, stdout contains "SUCCESS!" and "1 Passed"

go test ./pkg/ -count=1
# expect: exit 0
```

Run the spec's grep evidence (each must print the stated count):

```bash
grep -c 'runErr != nil && len(rows) == 0' pkg/steps_planning.go
# expect: 1

grep -c 'truncateTail(output, gateTailMaxBytes)' pkg/steps_planning.go
# expect: 1

grep -c 'no parseable findings → NeedsInput' pkg/steps_planning.go
# expect: 1

grep -c 'no parseable findings → Failed' pkg/steps_planning.go || true
# expect: 0

grep -c 'next HEAD' pkg/steps_planning.go
# expect: >= 1

grep -c 'AgentStatusNeedsInput' pkg/steps_planning_test.go
# expect: >= 1

grep -c 'no parseable findings → `needs_input`' docs/design.md
# expect: >= 1

sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -ci 'needs_input'
# expect: >= 1
```

Coverage check on the changed package:

```bash
go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'steps_planning|total'
# expect: the changed runInspection path covered; total not lower than before
```

Final validation — run ONCE after everything is green:

```bash
make precommit
# expect: exit 0
```

Evidence for the human reviewer (run after the container run; guarded so a zero-count never fails the executor):

```bash
git diff -U0 pkg/steps_planning_test.go | grep '^[-+].*AgentStatus' ; true
# expect: the removed AgentStatusFailed / NotTo(Equal(...NeedsInput)) pair and the added
# AgentStatusNeedsInput / NotTo(Equal(...Failed)) pair

git diff -U0 pkg/steps_planning_test.go | grep -c '^[-+].*Describe("gate target' ; true
# expect: 2 (one removed, one added)

git diff -U0 pkg/steps_planning_test.go | grep -cE 'Describe\("(clone auth failure|current-HEAD resolution failure|fabricated plan ID|prefix-collision|unparseable claude output|environment-claim|\.maintainer\.yaml consent|update_scope)' ; true
# expect: 0 (no sibling Describe block touched)

git diff pkg/steps_planning.go | grep -c 'no gate target found' || true
# expect: 0 (that branch untouched)
```
</verification>
