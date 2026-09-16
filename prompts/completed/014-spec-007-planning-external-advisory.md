---
status: completed
spec: [007-external-advisory-ingestion]
summary: Planning now reads and validates a task frontmatter advisory block in Go and admits it as an external:<source> row of the findings table, with external-vs-scanner ID collisions collapsed and spec 002's anti-fabrication guard untouched.
execution_id: github-update-go-agent-external-advisory-ingestion-exec-014-spec-007-planning-external-advisory
dark-factory-version: dev
created: "2026-09-16T19:50:00Z"
queued: "2026-09-16T17:44:39Z"
started: "2026-09-16T17:44:40Z"
completed: "2026-09-16T17:53:29Z"
branch: dark-factory/external-advisory-ingestion
---

# Planning ingests a validated external advisory from the task frontmatter

<summary>
- A task filed with an advisory block in its frontmatter is now understood by the planning step: the advisory is read and checked in Go before any scanner runs.
- The advisory becomes a row in the same findings table the model classifies, labelled with its source so a reader can tell it came from outside the repo.
- The table the model sees now has two provenances and the planning prompt says so, while still insisting the table is the only place advisory IDs may come from.
- A malformed advisory block stops the run immediately and asks the operator to correct it, naming the field and the value that failed; no scanner runs and the model is never called.
- A bad advisory ID still cannot be smuggled past the existing anti-fabrication check — that check itself is untouched.
- When the same advisory is reported both externally and by the repo's own scanners, the model sees exactly one row and that row carries a real fixed version.
- Tasks without an advisory block behave exactly as before, in planning and later in review.
- No new configuration, no opt-out flag, and no new dependency: one block shape, validated once, admitted once.
</summary>

<objective>
Give an externally-supplied advisory a path into planning: read the task frontmatter's `advisory` block in Go, validate it, and admit it as one row of the findings table the model already classifies — so an advisory that the repo's own scanners never reported can still drive a targeted fix, while spec 002's anti-fabrication guard (every plan ID must appear verbatim in the captured table) stays exactly as strict as it is today.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read before changing anything:

- `specs/in-progress/007-external-advisory-ingestion.md` — the approved spec. Desired Behaviors 1–5 are this prompt's scope; Constraints, Failure Modes, and Security/Abuse Cases are load-bearing.
- `pkg/steps_planning.go` — `planningStep`, `runInspection` (the findings-table construction site), `Run`, `needsInput`. `runInspection` detects gate targets, runs each through `GateRunner.RunTargetFull`, parses rows, filters operator-approved suppressions, renders the table into the `## Scanner Findings` context section, runs the Claude sub-call, and validates every plan ID against the table.
- `pkg/scanner_table.go` — `ScannerFinding`, `ScannerTable`, `Contains`, `Row`, `FilterSuppressed`, `scannerFindingIDRegexp`, `parseScannerOutput`, `scannerForTarget`, `validatePlanAgainstTable`, `renderScannerTable`, `loadSuppressedVulnIDs`.
- `pkg/plan_output.go` — `PlanOutput`, `PlanVuln`, `VulnActionFix`, `VulnActionPark`. Unchanged by this prompt.
- `pkg/gate_runner.go` — `GateRunner`, `gateTargetRegexp` (`^[A-Za-z0-9._-]+$`), `gateTailMaxBytes`, `truncateTail`.
- `pkg/steps_gh_token.go` — the `needsInput(msg)` / `failed(msg)` helpers (message-only results; the controller owns the envelope).
- `pkg/export_test.go` — the in-repo pattern for exposing unexported identifiers to the external `pkg_test` package.
- `pkg/steps_planning_test.go` — the fixture-workdir test shape (`setupFixture` writes a Makefile through the `CloneAtRefStub`; the real `pkg.NewOSExecGateRunner()` runs `make -C <workdir>`), and the two Describe blocks this prompt must not touch: `Describe("fabricated plan ID rejection")` and `Describe("prefix-collision plan ID rejection")`.
- `pkg/prompts/planning.md` and `pkg/prompts/prompts_test.go` — the planning prompt module and its assertions.
- `pkg/factory/factory.go` — `CreateAgent` calls `updatepkg.NewPlanningStep(...)`. `NewPlanningStep`'s signature does NOT change in this prompt.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; never `fmt.Errorf`, never `context.Background()` in `pkg/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, counterfeiter mocks.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-parse-pattern.md` — the repo-standard parse pattern (you are parsing a frontmatter block into a typed value).
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits (funlen 80, nestif 4, golines 100), license headers.

Two facts about the frontmatter mechanism, verified against `github.com/bborbe/agent v0.85.1` (read `$GOPATH/pkg/mod/github.com/bborbe/agent@v0.85.1/agent_markdown.go` and `agent_task-frontmatter.go` if you want to confirm):

```go
// agentlib (package lib): the frontmatter is a generic map parsed with
// gopkg.in/yaml.v3.
type TaskFrontmatter map[string]interface{}
// Markdown.Frontmatter TaskFrontmatter
```

`yaml.v3` decodes a nested YAML mapping into `map[string]interface{}` when the target is `interface{}` (see `stringMapType` in `gopkg.in/yaml.v3/decode.go`), and a YAML sequence into `[]interface{}`. So `md.Frontmatter["advisory"]` is a `map[string]interface{}` for a mapping, `[]interface{}` for a list, and some scalar type otherwise. `md.Frontmatter.String(key)` returns `(string, bool)` and is `ok=false` for a non-string value — use the map lookup, not `String`, for the block.

Run this first to see the current seams you will touch:

```bash
grep -rn 'runInspection\|validatePlanAgainstTable\|renderScannerTable' pkg/ --include='*.go' | grep -v _test.go
grep -n 'Scanner Findings' pkg/steps_planning.go pkg/prompts/planning.md
```
</context>

<requirements>
1. **Create `pkg/advisory.go`** in `package pkg`, with the 3-line BSD copyright header copied verbatim from `pkg/review_output.go`. It holds the frozen `advisory` block contract — the type, the parse+validate, and the row rendering.

   ```go
   // externalScannerPrefix labels a findings-table row that came from the task
   // frontmatter instead of from a gate target's output. The surviving label is
   // `external:<source>`. A gate target can never produce this prefix:
   // gateTargetRegexp (`^[A-Za-z0-9._-]+$`) admits no colon, so the
   // scannerForTarget fallback label can never be mistaken for an external row.
   const externalScannerPrefix = "external:"

   // advisoryIDRegexp is the anchored form of scannerFindingIDRegexp: the
   // advisory block's `id` must match one of the same alternatives in full
   // (GO-<year>-<n>, CVE-<year>-<n>, GHSA-<grp>-<grp>-<grp>), never as a
   // prefix. Deriving it from scannerFindingIDRegexp keeps the accepted ID
   // shape in exactly one place.
   var advisoryIDRegexp = regexp.MustCompile("^(?:" + scannerFindingIDRegexp.String() + ")$")

   // AdvisoryBlock is one validated externally-supplied advisory read from the
   // task frontmatter's `advisory` mapping. The block shape is frozen: exactly
   // the four keys id, package, fixed_version, source, all required and
   // non-empty. Unrecognized extra keys are ignored.
   type AdvisoryBlock struct {
       ID           string
       Package      string
       FixedVersion string
       Source       string
   }
   ```

   Implement:

   ```go
   // parseAdvisoryBlock reads the task frontmatter's `advisory` mapping and
   // validates it. (nil, nil) means the task carries no advisory block — the
   // planning table is then the gate output alone and the review rides the gate
   // re-run. A non-nil error means the block is present but malformed; the
   // caller decides how to fail (planning escalates needs_input, the review
   // fails closed). A partial block is never admitted.
   func parseAdvisoryBlock(ctx context.Context, md *agentlib.Markdown) (*AdvisoryBlock, error)

   // advisoryFinding renders an admitted advisory as one findings-table row.
   // The scanner label is `external:<source>`, so the model — and the captured
   // table — can tell an externally-supplied advisory from a scanner row.
   func advisoryFinding(a *AdvisoryBlock) ScannerFinding
   ```

   `parseAdvisoryBlock` behaviour, in this exact order (first failure wins, so the message is deterministic):

   - `raw, ok := md.Frontmatter["advisory"]`; `ok == false` → return `(nil, nil)`. The key being present with a `nil` value is NOT "absent" — it falls through to the mapping check below and is rejected.
   - Assert `map[string]interface{}`. Anything else — a list, a scalar, `nil` — is rejected with field `advisory`. This is the "list instead of mapping" rejection class.
   - `id`: assert string; `strings.TrimSpace` it; reject unless `advisoryIDRegexp.MatchString(id)`. Field `id`.
   - `package`: assert string; `strings.TrimSpace` it; reject when empty. Field `package`.
   - `fixed_version`: assert string; `strings.TrimSpace` it; reject unless `semver.IsValid(v)` from `golang.org/x/mod/semver`. Field `fixed_version`.
   - `source`: assert string; `strings.TrimSpace` it; reject when empty. Field `source`.
   - Return the block with the four trimmed values.
   - A missing key and an empty value are the same failure (the spec's contract is "required and non-empty"), so the raw value for a missing key is the `%v` of the `nil` map entry.
   - Store the trimmed values, not the raw ones — the rendered table row is built by `%s` formatting and must not carry stray whitespace.

   Every rejection returns an `errors.Errorf(ctx, ...)` from `github.com/bborbe/errors` whose message has exactly this shape (one line, no newline):

   ```
   invalid advisory frontmatter: field=<name> value=<raw> — <reason>
   ```

   with `<name>` one of `advisory`, `id`, `package`, `fixed_version`, `source`, `<raw>` the `fmt.Sprintf("%v", ...)` rendering of the raw map entry (so a non-string value such as a YAML float `1.2` shows as `1.2`, and a list shows as its `%v` form), and `<reason>` a short sentence naming the requirement. Use these reasons:

   - mapping check → `must be a single mapping with keys id, package, fixed_version, source`
   - id → `must match the advisory-ID shape (GO-<year>-<n>, CVE-<year>-<n>, GHSA-xxxx-xxxx-xxxx)`
   - package → `must be non-empty`
   - fixed_version → `must be a Go module version`
   - source → `must be non-empty`

   `advisoryFinding` returns `ScannerFinding{ID: a.ID, Package: a.Package, FixedVersion: a.FixedVersion, Scanner: externalScannerPrefix + a.Source}`.

2. **Add `ScannerTable.collapseExternalDuplicates`** to `pkg/scanner_table.go`:

   ```go
   // collapseExternalDuplicates resolves an external-advisory-versus-scanner ID
   // collision so the model sees exactly one row for an ID the external
   // advisory introduced. The row carrying a non-empty FixedVersion wins: the
   // first scanner row with a real fixed version beats the external row and
   // keeps its own scanner label (the repo's own scanner reported that fix);
   // otherwise the external row survives and every scanner row for that ID is
   // dropped. Rows whose ID has no external row are returned untouched — the
   // existing scanner-vs-scanner duplicate behavior is unchanged.
   func (t ScannerTable) collapseExternalDuplicates() ScannerTable
   ```

   Behaviour: find the first row whose `Scanner` has prefix `externalScannerPrefix` (at most one can exist — one advisory per task). If there is none, return the table unchanged. Otherwise take its `ID`, pick the winner as the first row with that ID (other than the external row) that carries a non-empty `FixedVersion`, defaulting to the external row, then rebuild the table keeping every row whose ID differs plus the winner, preserving the original order.

3. **Rewire `runInspection`** in `pkg/steps_planning.go`. Keep its signature (`ctx`, `md`, `workdir`, `updateScope` → `(*PlanOutput, ScannerTable, *agentlib.Result)`) and keep every existing step in place. Change the table construction only:

   - Immediately after the `len(targets) == 0` escalation and **before** the gate-target loop, parse and validate the block:

     ```go
     advisory, err := parseAdvisoryBlock(ctx, md)
     if err != nil {
         glog.V(2).Infof("planning: invalid advisory frontmatter — escalating: %v", err)
         return nil, nil, needsInput(err.Error())
     }
     ```

     This ordering is load-bearing: a malformed block must abort before any gate target runs, so the model is never invoked with a partial table and no gate target executes (spec AC4).

   - Build the table with the external row first, then append each gate target's parsed rows exactly as today:

     ```go
     table := ScannerTable{}
     if advisory != nil {
         table = append(table, advisoryFinding(advisory))
     }
     for _, target := range targets { /* unchanged body */ }
     ```

   - After the loop and **before** `loadSuppressedVulnIDs`, collapse the collision: `table = table.collapseExternalDuplicates()`. The existing suppression filter, the `plan.Vulns` suppression filter, and `validatePlanAgainstTable` stay byte-identical — this prompt widens the table's trusted source set, never the validator.

4. **Reword the `## Scanner Findings` preamble** in `runInspection` (the string literal that begins `"\n\n## Scanner Findings\n\nThe findings below were captured by Go from running the repo's own gate targets`). The new literal must:

   - name both provenances and contain the exact substring `external advisory`;
   - still contain the exact substring `ONLY source of advisory IDs` exactly once in the file (the invariant sentence survives verbatim);
   - no longer contain the substring `captured by Go from running the repo` (that gate-only claim is the one being replaced).

   Use this text for the preamble (one Go string literal on one line, as today — do not let a reflow split the phrase across lines):

   ```
   The findings below were captured by Go from the repo's own gate targets and, when the task carries one, from the validated external advisory in the task frontmatter — together they are the ONLY source of advisory IDs. Every vuln ID you report MUST appear in this table verbatim.
   ```

   The rest of the prompt assembly (workdir, target Go, update-scope section, `## Task`) is unchanged.

5. **Reword the `## Scanner Findings` bullet in `pkg/prompts/planning.md`** so the module tells the model the table has two provenances. Extend the first sentence of the bullet that currently reads ``- `## Scanner Findings` — the findings table Go captured by running the repo's own gate targets and parsing their output.`` to also name the validated external advisory from the task frontmatter, keeping it in the same bullet and on the same wrapped lines (so the sentence stays readable). The invariant lines below it — `It is the ONLY source of advisory IDs: every \`vulns[].id\` you report MUST be one of these IDs, copied verbatim. Never invent, guess, or modify an advisory ID, and never add a finding that is not listed here.` — stay byte-identical, and the phrase `ONLY source of advisory IDs` must still appear exactly once in the file. The model's contract is otherwise unchanged: it classifies rows and never authors an ID.

6. **Add the tests.** New code needs ≥80% statement coverage; every boundary the new code crosses gets a test that traverses it with the new value.

   a. **`pkg/advisory_test.go`** (new file, `package pkg_test`, joins the existing suite). Add the exports you need to `pkg/export_test.go` in the existing style (e.g. `ParseAdvisoryBlock = parseAdvisoryBlock`, `AdvisoryFinding = advisoryFinding`, and `CollapseExternalDuplicates = ScannerTable.collapseExternalDuplicates` — a method expression is fine).

   Build the fixtures with `agentlib.ParseMarkdown(ctx, ...)` on literal markdown so the test crosses the real frontmatter parse (never hand-construct a `TaskFrontmatter` map — that would skip the boundary this feature depends on). Cover:

   - **absent block** → `(nil, nil)`.
   - **valid block** → the four fields populated from a `advisory:\n  id: CVE-2026-12345\n  package: golang.org/x/text\n  fixed_version: v0.39.0\n  source: osv-feed\n` block; and the same block with an extra unrecognized key (e.g. `severity: HIGH`) still validates — extra keys are ignored.
   - **ID shape table** (a `DescribeTable`): `GO-2026-1234`, `CVE-2026-9999`, `GHSA-1234-5678-9012` accepted; `GO-2026-1234extra`, `FOO-2026-1`, `GO-26-1`, `` (empty) rejected with field `id`.
   - **list form** (`advisory:` followed by `- id: ...` entries) → error whose message contains `field=advisory` and the ID of one of the listed entries (the `%v` rendering of the list).
   - **scalar form** (`advisory: CVE-2026-12345`) → error naming `field=advisory`.
   - **missing / empty key** for each of `package`, `fixed_version`, `source` → error naming that field.
   - **unparseable `fixed_version`** (`1.2`, `not-a-version`) → error containing `field=fixed_version` and the raw value verbatim.
   - **non-string value** for `id` or `fixed_version` (e.g. a YAML float `fixed_version: 1.2`) → error naming the field and rendering the raw value (`1.2`).
   - **message contract**: every rejection message starts with `invalid advisory frontmatter: field=` and contains `value=`.
   - **`advisoryFinding`** renders `Scanner == "external:" + source` and copies the other three fields.

   b. **`pkg/scanner_table_test.go`** — add a `collapseExternalDuplicates` block:
   - table with no external row → returned unchanged (same length, same order), including a table with two scanner rows sharing one ID.
   - external row + a scanner row with the SAME id and an EMPTY fixed version → exactly one row survives, it is the external row, `Scanner` is `external:<source>`.
   - external row + a scanner row with the SAME id and a NON-EMPTY fixed version → exactly one row survives, its `FixedVersion` is the scanner's value and its `Scanner` is the scanner label (never `external:`).
   - external row + two scanner rows with the same id, both empty-fixed → exactly one row survives.
   - two DIFFERENT ids, one with an external row → the other id's rows are untouched.

   c. **`pkg/steps_planning_test.go`** — add a new `Describe` whose text contains the exact phrase `external advisory` (the spec's focused run is `-ginkgo.focus='external advisory'`), plus a fixture Makefile whose every gate target touches a marker file in the workdir, e.g.:

   ```
   .PHONY: check vulncheck
   check:
   	@touch gate-ran-marker
   	@echo '<scanner line for this fixture>'
   vulncheck:
   	@touch gate-ran-marker
   ```

   (the marker path is `<workdir>/gate-ran-marker`, where `<workdir>` is `filepath.Join(os.TempDir(), "github-update-go-test-task-1")` for the existing `planningTaskMD` fixture — the same path the environment-claim test already computes). Cover:

   - **AC1 — admitted and drives a fix**: gate-clean fixture (a Makefile whose recipes emit no advisory IDs and exit 0) plus a task whose frontmatter carries a valid `advisory` block; the fake runner returns a plan whose `vulns` contains the advisory's ID with `"action":"fix"` and the advisory's `fixed_version`. Assert: `runner.RunCallCount() == 1`; the captured prompt contains the rendered row `<id> | <package> | <fixed_version> | external:<source>` verbatim; and `agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")` carries that ID with `Action == pkg.VulnActionFix` and `FixedVersion` equal to the advisory's.
   - **AC2 — the widened table does not weaken the guard**: valid advisory block + a plan that adds a SECOND ID present in neither provenance → `agentlib.AgentStatusFailed`, message contains that ID, and `md.FindSection("## Plan")` reports `false` (no plan section written). Do NOT modify the existing `Describe("fabricated plan ID rejection")` or `Describe("prefix-collision plan ID rejection")` blocks — their text and assertions stay byte-identical.
   - **AC4 — malformed block rejected loudly, never dropped**: a `DescribeTable` over the malformed classes (ID outside the accepted shapes; empty `package`; unparseable `fixed_version`; a missing key; a list instead of a mapping). For every row assert: `result.Status == agentlib.AgentStatusNeedsInput`; the message contains the offending field name and the raw value; `runner.RunCallCount() == 0`; and `os.Stat(markerPath)` returns an error for which `os.IsNotExist(err)` is true (no gate target ran, so the row was never admitted).
   - **AC5 — duplicate IDs collapse, the fixed version survives**: two fixtures that collide on the same ID with different fixed versions.
     - Fixture A — the scanner row carries an EMPTY fixed version (an ID-bearing line in an unrecognized shape, e.g. `@echo 'CVE-2026-7001 affected in golang.org/x/net'`, which parses to a fallback row with no fixed version). With the advisory block present, the surviving row's fixed version is the advisory's and its label is `external:<source>`.
     - Fixture B — the scanner row carries a NON-EMPTY fixed version (an osv-shaped line, e.g. `@echo 'CVE-2026-7001 | golang.org/x/net | 1.26.5 | fixed v0.36.0'`). Run the fixture once WITHOUT the advisory block, capture the prompt, and extract the `## Scanner Findings` line for that ID; then run it WITH the advisory block and assert the line is byte-identical (differential assertion — never `external:<source>`).
     - Both fixtures assert exactly ONE line in the `## Scanner Findings` section contains that ID.
     - Extract the section by slicing the captured prompt from the LAST occurrence of the delimiter `"\n\n## Scanner Findings\n\n"` (`strings.LastIndex`) to the following `"\n\n## Task\n\n"`, then count lines containing the ID inside that slice. The exact delimiter matters: `pkg/prompts/planning.md` itself contains the backticked text `` `## Scanner Findings` ``, so a bare `strings.Index(prompt, "## Scanner Findings")` finds the module's own sentence, not the appended section. Count only inside the slice — the `## Task` section embeds the frontmatter, which also carries the advisory ID, so a whole-prompt count would be 2, not 1.
   - **no advisory block → unchanged**: the existing happy-path, park, no_update_needed, and fabricated-ID tests must keep passing with no edit to their assertions.

   d. **`pkg/prompts/prompts_test.go`** — ADD one assertion that `prompts.PlanningPrompt()` contains `external advisory`. Do NOT change the existing `It("treats the Go-captured Scanner Findings table as the only ID source", ...)` block or its `ONLY source of advisory IDs` assertion — that assertion must survive byte-identical.

7. **Before you finish**, re-run `<verification>` and confirm every command passes, then walk spec 007's Acceptance Criteria AC1, AC2, AC4, AC5, AC6 and AC11 against the change and confirm each one is satisfied. Then confirm the Do-Nothing-adjacent regressions are absent: a task without an `advisory` block produces exactly the same table, prompt preamble shape, and park/close decisions as before this change.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Spec 002's invariants hold unchanged: the model never authors an advisory ID, `validatePlanAgainstTable` stays a verbatim membership check against the captured table, and the park path (`needs_input` naming the verbatim row plus the suppression surfaces) is untouched.
- The `advisory` frontmatter block shape is frozen: a single mapping with the keys `id`, `package`, `fixed_version`, `source`. Unrecognized extra keys are ignored; the four documented keys are required and non-empty. The one `advisory` key is the whole interface — a second advisory written under a different key name is not the block and has no effect; two advisories in the `advisory` value (a list) are rejected at admission.
- Do NOT relax the anti-fabrication guard, and do NOT let an external advisory park the task — an advisory with no parseable fixed version is malformed input, rejected loudly at admission, never a park.
- `PlanOutput` / `PlanVuln` serialized keys keep their names and meanings. The external row rides the existing `scanner` field as `external:<source>` — no new plan field appears.
- Do NOT add config fields, opt-out flags, tunable thresholds, or a per-repo trust switch. Do NOT add a list form or multiple advisories per task.
- Do NOT add a stdlib-specific version-resolution path — an advisory naming the Go standard library is not special-cased here.
- No new module dependencies. `semver` comes from `golang.org/x/mod/semver`, part of the `golang.org/x/mod` module this repo already requires and imports (`pkg/changelog.go` uses `golang.org/x/mod/modfile`). Write the import in `pkg/advisory.go` before any `go mod tidy` runs.
- Gate target names still pass `gateTargetRegexp` before reaching `make` argv — unchanged.
- Error handling follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`, no `context.Background()` in `pkg/` non-test code.
- The existing suppression pipeline applies to the admitted row unchanged (design D4): an advisory whose ID the repo already suppresses is filtered out of the table like any other row.
- The existing scanner-vs-scanner duplicate behavior (two gate targets emitting the same ID, no external row for it) is unchanged; the dedupe covers only the external-versus-scanner collision.
- Do NOT modify `CHANGELOG.md` — prompt 3 of this spec creates the `## Unreleased` section and adds the bullet.
- Do NOT touch `pkg/steps_review.go`, `pkg/review_output.go`, `pkg/module_versions.go`, or `pkg/steps_execution.go` — the review-side verification is prompt 2 of this spec.
- Do NOT change `NewPlanningStep`'s signature, `pkg/factory/factory.go`, `main.go`, or `cmd/run-task/main.go` — no wiring changes are needed for this prompt.
- All existing tests pass except where this spec changes behavior explicitly; `make precommit` and `make test` stay green.
- New code needs ≥80% statement coverage; test error paths, not only the happy path.
</constraints>

<verification>
Run `make test` — all tests pass, including the new `pkg/advisory_test.go` cases, the new `collapseExternalDuplicates` cases, and the new planning Describe block.

Run the spec's focused run — it must exit 0 and print `SUCCESS!`:

```bash
go test -v ./pkg/ -count=1 -run TestSuite -args -ginkgo.focus='external advisory'
```

Then `make precommit` — must exit 0.

```bash
grep -c 'external advisory' pkg/steps_planning.go
# expect: >= 1

grep -c 'external advisory' pkg/prompts/planning.md
# expect: >= 1

grep -c 'ONLY source of advisory IDs' pkg/steps_planning.go
# expect: exactly 1

grep -c 'ONLY source of advisory IDs' pkg/prompts/planning.md
# expect: exactly 1

grep -c 'ONLY source of advisory IDs' pkg/prompts/prompts_test.go
# expect: exactly 1 (the pre-existing invariant assertion, untouched)

! grep -q 'captured by Go from running the repo' pkg/steps_planning.go
# expect: exit 0 (the old gate-only claim is gone)

grep -c 'Describe("fabricated plan ID rejection")\|Describe("prefix-collision plan ID rejection")' pkg/steps_planning_test.go
# expect: 2 (both pre-existing Describe blocks still present)

grep -rn 'parseAdvisoryBlock\|advisoryFinding\|collapseExternalDuplicates' pkg/ --include='*.go' | grep -v _test.go
# expect: pkg/advisory.go (definitions), pkg/scanner_table.go (collapseExternalDuplicates), pkg/steps_planning.go (the one call site of each)

go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'advisory|scanner_table|steps_planning|total'
# expect: the new advisory.go functions at or near 100%; scanner_table and steps_planning above 80%
```
</verification>
