---
status: approved
approved: "2026-11-24T20:52:41Z"
branch: dark-factory/vuln-scoped-targeted-updates
---

## Summary

- `github-vuln-watcher` already files every vuln task with a `vulns:` frontmatter list, but the agent reads only the single-advisory `advisory:` block, so a vuln task falls through to the ordinary update path and runs `go get -u ./...`.
- The full sweep takes every module to `@latest`, a superset of every listed finding's `fixed_version`, so the resulting PR carries unrelated bumps and cannot be read as "these advisories are fixed" — the exact defect the `advisory:` skip was introduced to prevent, one layer up.
- This spec makes a non-empty `vulns:` list select a **vuln-scoped** mode: the deterministic sweep is skipped, the targeted `go get <pkg>@<fixed>` bumps are the whole change, and the repo's gate targets stay the verdict.
- The scope is signalled by the task, not inferred by the model: the list is parsed and validated in Go before any prompt is built, and a malformed list fails closed rather than degrading into a full sweep.
- A vuln-scoped task never widens to a full update silently: when no targeted bump clears a listed finding the run ends with the red gate target and its output, and the task goes back to a human.

## Problem

`github-update-go-agent` has two update modes today: the default, which runs `go get -u ./...` plus `go mod tidy` in Go before the model is called, and a second selected by a single-advisory `advisory:` frontmatter block, on which the deterministic sweep is deliberately skipped because sweeping every module to `@latest` overshoots the advisory's `fixed_version` and destroys the "this advisory is fixed" reading of the diff. A vuln task filed by `github-vuln-watcher` carries neither — it carries `vulns:`, a list of advisory IDs, plus `vuln_count`, written so the task page names what is red — and nothing in the agent reads either field, so `runBulkUpdate` parses `advisory`, finds none, and runs the sweep. The PR that follows bumps every outdated module in the repo alongside the ones the advisories actually need; the prototype recorded 35 fleet vuln PRs, all of them full bumps. Reviewing one means reviewing an unrelated dependency upgrade attached to a security fix, which is the review risk the operator's design ("if a vuln occurs … we update directly … but only the needed deps") exists to remove. The information needed to scope the bump is already in the run — planning parses the repo's own gate output into a findings table whose rows carry the package and the `fixed_version` (`pkg/scanner_table.go`); what is missing is the signal that this task is a vuln task at all.

## Goal

A task whose frontmatter carries a non-empty `vulns:` list runs in vuln-scoped mode end to end. Planning treats outdated-but-not-vulnerable dependencies as out of scope, so a task with no current findings and no toolchain bump is `no_update_needed` rather than a ready run with nothing to do. Execution skips the deterministic sweep, is told the listed IDs and that the targeted bumps are the whole change, and bumps only the modules behind the plan's fix-action findings. The repo's gate targets remain the sole verdict, exactly as today. When a targeted bump does not clear a finding, the run ends with the red target and its output and opens no PR — it never falls back to a full update. A task without `vulns:` behaves byte-for-byte as it does today, including the existing `advisory:` path.

## Non-goals

- Do NOT change lane routing, the `assignee` a vuln task is stamped with, or any deployed image pin — that is the sibling task "Vuln Tasks Route to the Dedicated Vuln-Fix Lane" (operator vault).
- Do NOT add or change a monthly full-update cadence — that is the sibling task "Full Dependency Updates Run Monthly Per Repo" (operator vault).
- Do NOT change how vulnerabilities are detected, which scanners run, or the gate targets a repo declares.
- Do NOT change the `advisory:` block contract, its validation, or its ai_review installed-version check. A task carrying both `advisory:` and `vulns:` keeps the advisory path.
- Do NOT change the `## Plan` / `## Result` / `## Review` JSON contracts or the `ReviewChecks` key names and meanings.
- Do NOT add a config field, environment variable, opt-out flag, or per-repo switch — the mode is selected by the task's own `vulns:` list and nothing else.
- Do NOT make the agent write a suppression (`.trivyignore`, `.osv-scanner.toml`, `VULNCHECK_IGNORE`) to clear a finding it cannot bump.
- Do NOT add auto-merge for vuln PRs — a human merge stays by design.

## Assumptions

- `github-vuln-watcher` writes `vulns:` as a YAML list of advisory-ID strings and `vuln_count` as an integer. This is a contract in another repo; it is confirmed on real fleet tasks (for example `vuln_count: 8` with eight `GO-2026-*` entries) but this repo cannot enforce it. The parse rejects anything else loudly rather than guessing.
- `vuln_count` is informational only and is never consulted for a decision — `vulns:` is the sole signal, so a watcher that stops writing the count changes nothing here.
- `advisory:` outranks `vulns:` when both are present, because the advisory block names one exact package and version while the list names only IDs.
- The repo's gate targets remain the only verdict for a finding; this spec narrows what is bumped, never what proves the bump worked.

## Acceptance Criteria

- [ ] **AC1 — a `vulns:` task skips the deterministic sweep and names the IDs:** a task with a non-empty `vulns:` list and no `advisory:` block executes with `bulk.RunCallCount()` equal to `0`, and the prompt handed to the execution model contains `## Bulk update — SKIPPED`, contains `vuln-scoped`, contains each of two distinct listed IDs verbatim (`GO-2021-0113` and `CVE-2021-38561`), and contains neither `## Bulk update — ALREADY DONE` nor `## Bulk update — DID NOT RUN` — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='vuln-scoped' | tee /tmp/ac1.log | grep -q 'SUCCESS!'` exits 0 **and** `grep -qE 'Ran [1-9][0-9]* of' /tmp/ac1.log` exits 0. The spec-count assertion is load-bearing, not decoration: a `-ginkgo.focus` string matching zero specs still prints `SUCCESS!` and exits 0, so the first command alone is satisfied by a tree containing no vuln-scoped case at all. The focused case asserts the call count and all six prompt substrings, so a section naming only the count or only the first ID fails.
- [ ] **AC2 — a task without `vulns:` is unchanged:** the pre-existing default-path case still runs the sweep — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='runs the bulk dep update by default'` exits 0 and asserts `bulk.RunCallCount()` is `1`. The pre-existing case is not edited — evidence: `BASE=$(git merge-base HEAD origin/master); test "$(git diff "$BASE" -- pkg/steps_execution_test.go | grep -c 'runs the bulk dep update by default')" = 0` exits 0, paired with a positive control `test -n "$(git diff "$BASE" --stat -- pkg/steps_execution_test.go)"` so an empty diff cannot be misread as "unmodified". The diff is anchored on the merge base, never on `HEAD`, because the implementer commits and a `HEAD`-relative diff would be empty on a committed tree.
- [ ] **AC3 — a malformed `vulns:` list fails closed at planning:** each malformed class — a scalar instead of a list, a list element that is not a string, and an element that does not match the accepted advisory-ID shapes — ends planning with `AgentStatusNeedsInput` whose message names the field (`vulns`) and the offending raw value, invokes the model zero times (`runner.RunCallCount()` is `0`), and runs no gate target — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='malformed vulns' | tee /tmp/ac3.log | grep -q 'SUCCESS!'` exits 0 and `grep -qE 'Ran [1-9][0-9]* of' /tmp/ac3.log` exits 0. The table-driven focused cases assert the status, the message substrings, the run count, and that the fixture's gate-target marker file does not exist after the run (`os.Stat` returns a not-exist error); every gate target in the fixture Makefile touches that marker, so its absence proves no gate target ran.
- [ ] **AC4 — a targeted bump that does not clear a finding never widens:** on a vuln-scoped task whose gate target stays red through the single repair attempt, the run ends `AgentStatusFailed` naming the red target and its output, opens no PR (`gh.CreatePRCallCount()` is `0`), and the execution prompt carries the instruction that the run must not fall back to a full update — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='never widen' | tee /tmp/ac4.log | grep -q 'SUCCESS!'` exits 0 and `grep -qE 'Ran [1-9][0-9]* of' /tmp/ac4.log` exits 0. The focused case sets the gate to stay red on both runs and asserts the status, the message prefix, the create-PR call count, and that the captured prompt contains `never widen` and does not contain `## Bulk update — ALREADY DONE`.
- [ ] **AC5 — planning does not treat stale non-vulnerable deps as work on a vuln-scoped task:** a vuln-scoped task whose plan has no fix-action finding and no `go_bump`, but whose inspection reports `dep_updates_expected: true`, closes as `no_update_needed` with `has_work: false` — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='vuln-scoped planning' | tee /tmp/ac5.log | grep -q 'SUCCESS!'` exits 0 and `grep -qE 'Ran [1-9][0-9]* of' /tmp/ac5.log` exits 0. Three focused cases assert the `## Plan` section's `outcome` and `has_work` fields: no fix-action finding and no `go_bump` → `no_update_needed` / `false`; one fix-action finding → `ready` / `true`; and a `go_bump` alone → `ready` / `true`, so the filter cannot pass by discarding all work and cannot pass by discarding the toolchain bump either.
- [ ] **AC6 — a task carrying both blocks keeps the advisory path:** a task with an `advisory:` block and a `vulns:` list executes as advisory-driven — evidence: `go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='advisory priority' | tee /tmp/ac6.log | grep -q 'SUCCESS!'` exits 0 and `grep -qE 'Ran [1-9][0-9]* of' /tmp/ac6.log` exits 0. The pre-existing `skips the bulk dep update when the task carries an advisory block` case passes unmodified (`BASE=$(git merge-base HEAD origin/master); test "$(git diff "$BASE" -- pkg/steps_execution_test.go | grep -c 'carries an advisory block')" = 0` exits 0), and a new focused case with both fields asserts the prompt contains ``carries an `advisory` block`` and `NotTo(ContainSubstring("vuln-scoped"))`. The negative assertion is load-bearing: `advisory` and the advisory's ID are already in the prompt today, so only asserting their presence would pass on a build where the vuln section wrongly wins priority.
- [ ] **AC7 — `docs/design.md` and `CHANGELOG.md` carry the mode:** the §3.3 task-format block documents the optional `vulns:` list with its accepted shapes and a YAML sample, the planning `Input` row names `vulns`, the execution `Side effects` row states the sweep is skipped on a vuln-scoped task, and §5.1 Inputs names the field — evidence, each anchored, never a whole-file count: `sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'vulns'` returns ≥1 **and** the `vulns:` sample range matches `GO-<year>-<n>` (`sed -n '/^vulns:/,/^[a-z]/p' docs/design.md | grep -c 'GO-<year>-<n>'` returns ≥1, anchored on the new sample rather than the whole §3.3 range, which already carries that token in the `advisory` example and would otherwise pass untouched) **and** a `vulns:` sample line exists (`sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c '^vulns:'` returns ≥1); `grep -n '^| Input | frontmatter .repo.' docs/design.md | grep -c 'vulns'` returns `1`; `grep -n '^| Side effects | worktree' docs/design.md | grep -c 'vuln-scoped'` returns `1`; `sed -n '/^## 5.1 Inputs/,/^## 5.2/p' docs/design.md | grep -c 'vulns'` returns ≥1. `CHANGELOG.md` has no `## Unreleased` heading today (its first heading is `## v0.26.1`), so the implementer **creates** `## Unreleased` above `## v0.26.1` rather than appending under a released heading — evidence: `grep -c '^## Unreleased' CHANGELOG.md` returns `1` and `sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -ci 'vuln-scoped'` returns ≥1.
- [ ] **AC8 — the repo stays green:** `make precommit` exits 0 and `make test` exits 0 — evidence: both commands exit 0.

No new scenario. Both halves of the change are reachable from the existing Ginkgo suites with the current seams (a fixture Markdown plus the fake `BulkUpdater` / `ClaudeRunner` / `GateRunner` for execution; a fixture workdir for planning), and this repo has no scenario harness. The end-to-end "the PR diff is scoped" outcome is runtime-only and is carried by the non-gating operator check below.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

```
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='vuln-scoped' | tee /tmp/ac1.log | grep -q 'SUCCESS!'
grep -qE 'Ran [1-9][0-9]* of' /tmp/ac1.log
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='malformed vulns' | tee /tmp/ac3.log | grep -q 'SUCCESS!'
grep -qE 'Ran [1-9][0-9]* of' /tmp/ac3.log
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='never widen' | tee /tmp/ac4.log | grep -q 'SUCCESS!'
grep -qE 'Ran [1-9][0-9]* of' /tmp/ac4.log
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='vuln-scoped planning' | tee /tmp/ac5.log | grep -q 'SUCCESS!'
grep -qE 'Ran [1-9][0-9]* of' /tmp/ac5.log
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='advisory priority' | tee /tmp/ac6.log | grep -q 'SUCCESS!'
grep -qE 'Ran [1-9][0-9]* of' /tmp/ac6.log
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='runs the bulk dep update by default' | tee /tmp/ac2.log | grep -q 'SUCCESS!'
grep -qE 'Ran [1-9][0-9]* of' /tmp/ac2.log
go test ./pkg/ -count=1
BASE=$(git merge-base HEAD origin/master); test "$(git diff "$BASE" -- pkg/steps_execution_test.go | grep -c 'runs the bulk dep update by default')" = 0
BASE=$(git merge-base HEAD origin/master); test "$(git diff "$BASE" -- pkg/steps_execution_test.go | grep -c 'carries an advisory block')" = 0
BASE=$(git merge-base HEAD origin/master); test -n "$(git diff "$BASE" --stat -- pkg/steps_execution_test.go)"
sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'vulns'
sed -n '/^vulns:/,/^[a-z]/p' docs/design.md | grep -c 'GO-<year>-<n>'
sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c '^vulns:'
grep -n '^| Input | frontmatter .repo.' docs/design.md | grep -c 'vulns'
grep -n '^| Side effects | worktree' docs/design.md | grep -c 'vuln-scoped'
sed -n '/^## 5.1 Inputs/,/^## 5.2/p' docs/design.md | grep -c 'vulns'
grep -c '^## Unreleased' CHANGELOG.md
sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -ci 'vuln-scoped'
make precommit
make test
```

Expected: every focused `go test` line exits 0 and its paired `Ran [1-9][0-9]* of` assertion exits 0 — the pair is what proves the focus string matched a real spec, since a miss still prints `SUCCESS!`; the full `go test ./pkg/` exits 0; the two `git diff … | grep -c` comparisons exit 0 (the pre-existing cases were not edited) and the positive control exits 0 (the test file was touched); each design.md probe prints the stated count (`≥1` for the §3.3 `vulns` range, the `vulns:` sample range and the §5.1 range, exactly `1` for the two row lookups); `grep -c '^## Unreleased'` prints `1` and the CHANGELOG range contains a bullet naming the mode; `make precommit` and `make test` both exit 0.

### Operator-executable (host, after merge + deploy) — recorded for the deploy tail, does NOT gate verification

- After the image is rolled out to a lane that takes vuln tasks, file one real vuln task for a repo whose gate output reports at least one finding with a populated `fixed_version`. Expected: the opened PR's `go.mod` diff changes only the modules the findings name (plus whatever `go mod tidy` must reconcile), `## Plan` lists those findings as `action: "fix"`, and `## Review` shows the gate green. The changed-module set is compared against the finding packages by hand — this is the human check the SC's "only the needed modules" claim rests on.
- File the same task against a repo where the finding's `fixed_version` cannot be reached (a withdrawn version). Expected: no PR, the task ends failed naming the red gate target, and no full update is attempted.

## Desired Behavior

1. The agent parses the task frontmatter's optional `vulns` list in Go, alongside the existing `advisory` parse, and validates it: the value must be a list, every element must be a string, and every element must match one of the accepted advisory-ID shapes (`GO-<year>-<n>`, `CVE-<year>-<n>`, `GHSA-xxxx-xxxx-xxxx`). An absent or empty value means "not vuln-scoped" and nothing changes. `vuln_count` is informational and is never read for a decision. A value that fails validation ends the run loudly rather than being ignored: planning returns `needs_input` naming the field and the offending raw value before any gate target runs, and execution — which never escalates `needs_input` — fails closed with the same message. A malformed list is never treated as absent, because "absent" means "run the full sweep".
2. Planning applies the vuln scope to the plan before classifying it: on a vuln-scoped task `dep_updates_expected` does not contribute to `has_work`, so a task with no fix-action finding and no `go_bump` closes as `no_update_needed`. `go_bump` stays in scope — a stdlib finding is fixed by the directive bump, and the directive bump is mandatory in every update PR. The fix-action findings stay in scope and drive `ready`.
3. Execution skips the deterministic `go get -u ./...` sweep on a vuln-scoped task, for the same reason the advisory path skips it: the sweep is a superset of every listed `fixed_version`, so the targeted pin that follows becomes a no-op or a downgrade. The `advisory` block, when present, keeps priority — a task carrying both runs the advisory path.
4. The execution prompt's bulk-update section renders a third SKIPPED variant for the vuln scope. It names the task's listed IDs verbatim, states that the targeted `go get <pkg>@<fixed>` bumps in update-sequence step 4 are the whole change, and states explicitly that the run must not widen to a full update — not `go get -u ./...`, not `go mod tidy -u`, and no module outside the plan's **fix-action** findings (the plan's `vulns` array also carries `park` rows, and a parked module is not a bump target) — and it carries the literal phrase `never widen`, which AC4 asserts, so the AC and this behaviour cannot drift apart.
5. The repo's gate targets remain the whole verdict, unchanged. A red target after the single bounded repair attempt ends the run `failed` with the target and its output tail, opens no PR, and is the point at which a vuln-scoped task returns to a human — the agent never answers a stubborn finding by widening the bump.
6. A task without `vulns:` — including every task carrying only `advisory:`, and every ordinary Go-version task — behaves exactly as today.

## Constraints

- The `vulns` frontmatter shape is the watcher's: a list of advisory-ID strings. The agent reads it and never writes it; `vuln_count` is not consulted.
- `PlanOutput`, `PlanVuln`, `ResultOutput` and `ReviewChecks` keep their serialized key names and meanings. No new plan, result or review field appears.
- The existing `appliesScope` filter keeps its meaning for `update_scope`; the vuln-scope filter is a separate, additive step, so a `golang`-scoped task and a `deps`-scoped task behave as they do today.
- The `advisory` path is untouched: its parse, its validation classes, its dedupe rule, and its ai_review installed-version check all stay as shipped in spec 007.
- The sweep is still skipped in Go, never by prompt instruction alone — the model's inability to background a long `go get` is what the `BulkUpdater` seam exists for, and a vuln-scoped task must not reintroduce a model-issued sweep.
- Error handling follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`, no `context.Background()` in `pkg/`.
- No new module dependencies — ID-shape validation reuses the existing regexp.
- The new execution-phase cases live in `pkg/steps_execution_test.go` and the new planning cases in `pkg/steps_planning_test.go`; AC2's positive control asserts that the former file was touched, so a correct implementation that parks the new cases in a separate file would fail verification.
- `make precommit` and `make test` stay green; every existing test passes unmodified except where this spec changes behaviour explicitly.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery | Reversibility |
|---|---|---|---|---|
| `vulns:` is a scalar, a mapping, or carries a non-string element | Planning ends `needs_input` naming `vulns` and the raw value; no gate target runs, the model is never invoked | `## Result` message names the field and value | Correct the task frontmatter and re-delegate; the next run's `## Plan` is built from the corrected list | None needed — nothing was written outside the workdir |
| `vulns:` carries an element outside the accepted ID shapes | Same as above — a malformed list is never silently dropped, because dropping it would select the full sweep | `## Result` message names the offending element | Correct or drop the ID and re-delegate; the corrected run's `## Result` no longer names it | None needed |
| `vulns:` is valid but the repo's gate output no longer reports any of those IDs | The plan has no fix-action finding; if there is also no `go_bump` the task closes `no_update_needed`; otherwise it proceeds with the toolchain bump only | `## Plan` `outcome` / `has_work` | Nothing — the findings were already fixed | None — no branch or PR created |
| A listed finding has no reachable `fixed_version` | Planning classifies it `park` (the existing rule: empty `fixed_version` only), the task escalates `needs_input` with the verbatim row and the suppression surfaces | `## Result` message | Operator suppresses with justification or handles it by hand; the re-delegated task's `## Plan` shows the row gone or parked by decision | None needed |
| A targeted bump does not clear a finding and the gate stays red | The run ends `failed` naming the red target and output tail; no PR; never a widened sweep | `## Result` message + `failed` | Operator inspects the diff and the finding, then fixes by hand or re-delegates; the re-delegated run either names the same red target or opens a PR whose `go.mod` diff is limited to the named modules | Reversible — no PR opened, nothing pushed |
| The module fetch for a targeted bump fails (transport, withdrawn version, proxy error) | The run ends `failed` naming the command failure; it is never retried as a full sweep | `## Result` message naming the `go get` failure | Re-delegate; a transient failure clears on the next run, a withdrawn version needs a different `fixed_version` | Reversible — no PR opened |
| A vuln-scoped task also carries `advisory:` | The advisory path wins: sweep skipped, the advisory's targeted pin is the change | `## Result` / prompt section naming the advisory, and no `vuln-scoped` section | None — deliberate priority | None needed |
| `vulns:` present but empty (`vulns: []`) | Treated as not vuln-scoped: ordinary path, sweep runs | `## Bulk update — ALREADY DONE` in the prompt | File the list the watcher would have written, or accept the full update | None needed |
| The PR is opened and later found to carry an unrelated bump | The diff is the evidence; the gate was green but the scope claim was wrong | PR `go.mod` diff against the finding packages | Close the PR; the agent never auto-merges, so nothing lands without a human | Reversible — an unmerged branch and draft PR |

## Security / Abuse Cases

- **Who controls what.** `vulns:` is task frontmatter, behind the same trust boundary as `repo`, `clone_url` and `ref`: anyone who can file a task for this agent can set it. The field adds no new reach — it narrows what the agent does, it does not widen it.
- **What reaches a command.** The IDs themselves never reach a shell command; they are rendered into the prompt as text and are validated against the ID-shape regexp first. The package and version that do reach `go get` come from the scanner findings table, exactly as they do today.
- **What fails closed.** A malformed `vulns:` fails the run rather than degrading to the full sweep, so a typo cannot silently restore the behaviour this spec removes. A red gate ends the run rather than triggering a wider bump.
- **No new secrets.** The field carries advisory IDs only, and nothing new is logged beyond those IDs.
- **No privilege change.** The mode does strictly less work than the default path: no extra command, no extra write, no extra network call.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Parse and validate `vulns:` in Go (shared parse for planning and execution), planning's vuln-scope filter on `has_work`, `needs_input` on a malformed list, unit tests | 1, 2 | AC3, AC5 | — |
| 2 | Execution: skip the sweep on a vuln-scoped task, the third SKIPPED prompt section naming the IDs and forbidding a widening, the advisory-priority rule, unit tests | 3, 4, 5, 6 | AC1, AC2, AC4, AC6 | prompt 1 (shares the parse and the validation classes) |
| 3 | Documentation tail: `docs/design.md` §3.3 task format, planning `Input` row, execution `Side effects` row, §5.1 Inputs; create `## Unreleased` in `CHANGELOG.md` with its bullet | — | AC7, AC8 | prompts 1–2 |

Rationale: prompt 1 establishes the parse and the validation classes both phases share, so prompt 2 can rely on a single definition of "vuln-scoped". Prompt 3 documents shipped behaviour and must follow both. Splitting planning from execution keeps each prompt inside one code layer (`pkg/update_scope.go` plus `pkg/steps_planning.go` versus `pkg/steps_execution.go` plus `pkg/prompts/execution.md`).

## Do-Nothing Option

Vuln tasks keep taking the full-update path. Every security PR carries an unrelated dependency upgrade, so reviewing one means reviewing a sweep, and the "this advisory is fixed" reading of the diff is unavailable — which is precisely the review risk the operator's design names. The `vulns:` list the watcher already writes stays decorative: the agent is told what is red and ignores it. The targeted path exists and works, gated behind a single-advisory `advisory:` block that no watcher produces, so the capability is present but unreachable for every task the fleet actually files. Doing nothing is acceptable only while vuln PRs are rare enough that a human re-deriving the diff by hand is cheaper than the change — which the prototype's 35 full-bump PRs already contradicts.
