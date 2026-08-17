---
status: completed
spec: [002-bug-fabricated-vulnerability-advisories]
summary: 'Moved planning gate detection into Go: added GateRunner.RunTargetFull with full-output capture, built pkg/scanner_table.go (Makefile target detection, three-shape scanner-output parser, verbatim plan-ID validation, table rendering, park-row escalation), rewired NewPlanningStep/runInspection around the captured findings table, rewrote planning.md around the Scanner Findings section, reworked step/prompt/gate tests, and updated CHANGELOG; make precommit exits 0'
execution_id: github-update-go-agent-fabricated-advisories-exec-005-spec-002-go-scanner-capture-parse-validate
dark-factory-version: dev
created: "2026-08-17T20:35:00Z"
queued: "2026-08-17T18:51:44Z"
started: "2026-08-17T18:51:46Z"
completed: "2026-08-17T19:10:43Z"
---

# Go-side scanner capture, parse, and plan-ID validation for the planning phase

<summary>
- The planning step stops trusting the model for advisory IDs: it detects the repo's gate targets by reading the Makefile in Go, runs each detected target through the existing gate runner, and parses the raw output into a findings table.
- The parsed table is the ground truth for the whole planning step: it is embedded in the planning prompt as the only source of advisory IDs, and every `vulns[].id` in the model's plan is validated against it before any park decision.
- A fabricated ID, or an ID that only shares a prefix with a real finding, fails the planning run loudly with an error naming the ID — it can never reach the park or no-fix suppression path.
- A park escalation now carries the verbatim scanner row (id, scanner, fixed version) from the parsed table instead of the model's prose reason, so an operator's suppression decision is made against the real finding.
- Gate-target detection moves out of the planning prompt into Go; the prompt no longer tells the model to detect or run gate targets, and the plan's `gate_targets` field is overwritten from the Go read.
- A gate target that exits non-zero with no parseable findings is treated as an error, not as a clean gate — an empty-on-error output is never read as "clean".
- The `GateRunner` seam gains a full-output capture method; the planning prompt is rewritten around the `## Scanner Findings` section; the `## Plan` JSON contract itself is unchanged.
- Unit and integration tests drive the new detection, parsing, validation, and routing paths, including the fabricated-ID and prefix-collision rejection cases.
</summary>

<objective>
Move the factual step of the planning phase into Go: detect the repo's gate targets from the Makefile, run them, capture the full raw scanner output, parse it into a findings table that is the only source of advisory IDs for the model, and reject any plan whose vuln IDs are not verbatim members of that table — so a fabricated advisory ID is impossible to act on.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read before changing anything:
- `docs/design.md` — design decisions D1 and D4, `## 4.3 Per-phase decisions` (planning), `## 4.4 State passing + invariants` (the `Result.vulns_fixed ⊆ {v.id | v ∈ Plan.vulns, action=fix}` invariant presupposes real IDs), and `## Suppression surfaces`. The agent never writes ignore entries; parking stays the only agent-side move on an unfixable finding (`suppressionSurfacesHint`).
- `pkg/steps_planning.go` — `planningStep`, `NewPlanningStep`, `runInspection`, `parkFindings`, `parkMessage`, `Run`. Note `NewPlanningStep` currently has NO gate parameter; you will add one.
- `pkg/gate_runner.go` — the `GateRunner` interface, `osExecGateRunner`, `gateTargetRegexp` (`^[A-Za-z0-9._-]+$`), `gateTailMaxBytes`, `truncateTail`. You will add a full-output method.
- `pkg/plan_output.go` — `PlanOutput` and `PlanVuln` (the `## Plan` JSON contract; it must stay byte-identical in shape).
- `pkg/prompts/planning.md` and `pkg/prompts/prompts.go` — the planning prompt module and `PlanningPrompt()`.
- `pkg/prompts/prompts_test.go` — the existing planning-prompt assertions (one of them, `ContainSubstring("never hardcode")`, must change).
- `pkg/steps_planning_test.go` — the existing planning step tests; they will be reworked to a fixture-workdir shape.
- `pkg/factory/factory.go` — `CreateAgent` constructs `updatepkg.NewPlanningStep(planningRunner, gitOps, ghToken, updatepkg.NewGhInstallationScope(ghToken))`; it already receives `gateRunner updatepkg.GateRunner`.
- `pkg/steps_gh_token.go` — `needsInput(msg)` and `failed(msg)` helper shapes (message-only results; the controller owns the envelope).
- `pkg/git/os_exec_git_ops.go` — `RedactToken` (not needed in this prompt, but read to know the package-qualified call form used elsewhere).
- `pkg/export_test.go` — the in-repo test-export pattern (`package pkg` internal test file exposing unexported identifiers), used for the parser/validator tests.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` usage; never `fmt.Errorf`, never `context.Background()` in `pkg/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-parse-pattern.md` — the repo-standard parse pattern (you are parsing scanner output into typed rows).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` — counterfeiter mocks are regenerated by `make generate` (part of `make precommit`); never hand-edit `mocks/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — changelog entry style.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.

Run this first to see every current gate/planning seam you will touch:

```bash
grep -rn 'NewPlanningStep\|RunTarget\|GateRunner' pkg/ --include='*.go' | grep -v _test.go
grep -n 'Detect gate targets\|Run the detected scanner' pkg/prompts/planning.md
```
</context>

<requirements>
1. **Extend `GateRunner` with a full-output capture method** in `pkg/gate_runner.go`. Add to the interface (keeping the existing `RunTarget` doc comment):

   ```go
   // RunTargetFull runs `make <target>` in workdir and returns the FULL
   // combined output (no truncation), the process exit code (0 on success),
   // and a non-nil error when the target failed or could not be started.
   // Planning uses this to capture the complete raw scanner output; the
   // bounded RunTarget tail remains for failure messages.
   RunTargetFull(ctx context.Context, workdir, target string) (output string, exitCode int, err error)
   ```

   Implement it on `osExecGateRunner`: validate the target against `gateTargetRegexp` exactly as `RunTarget` does (same `errors.Errorf` invalid-target error — this is the no-injection-regression invariant; the targets now come from a Go Makefile read but must still be regexp-validated before they reach `make` argv), then run `exec.CommandContext(ctx, "make", "-C", workdir, target)` with `cmd.Env = os.Environ()`, and return `string(out)` (the full `CombinedOutput`) plus the same exit-code/error handling as `RunTarget`. Do NOT truncate. The existing `//counterfeiter:generate` directive at the top of `GateRunner` needs no change — the mock at `mocks/gate_runner.go` is regenerated by `make generate` (part of `make precommit`) and must NOT be hand-edited.

2. **Create `pkg/scanner_table.go`** in `package pkg` with the 3-line BSD copyright header copied from `pkg/review_output.go`. It holds the parsed-findings ground-truth model, the Makefile gate-target detection, and the scanner-output parser:

   ```go
   // scannerFindingIDRegexp anchors the row parse on advisory IDs. Each
   // alternative requires the full ID shape (GO-<year>-<n>, CVE-<year>-<n>,
   // GHSA-<grp>-<grp>-<grp>) so a longer ID like GO-2026-50260 is captured in
   // full and never conflated with the prefix GO-2026-5026.
   var scannerFindingIDRegexp = regexp.MustCompile(`GO-\d{4}-\d+|CVE-\d{4}-\d+|GHSA-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}`)

   // ScannerFinding is one parsed finding row from a gate target's raw output.
   type ScannerFinding struct {
       ID           string
       Package      string
       FixedVersion string // empty when the scanner reports no fix
       Scanner      string // govulncheck | osv-scanner | trivy | <target> fallback
   }

   // ScannerTable is the parsed ground-truth findings table for a planning run,
   // accumulated across every detected gate target.
   type ScannerTable []ScannerFinding

   // Contains reports whether id appears verbatim in the table (exact match).
   func (t ScannerTable) Contains(id string) bool

   // Row returns the first row whose ID equals id exactly.
   func (t ScannerTable) Row(id string) (ScannerFinding, bool)
   ```

   `Contains` and `Row` are plain `for … range` loops with `==` comparison — no substring matching, no case folding.

3. **Implement `detectGateTargets`** in `pkg/scanner_table.go`:

   ```go
   // knownGateTargets is the deterministic preference order of repo gate
   // targets the planning step looks for (design § 4.3 planning).
   var knownGateTargets = []string{"precommit", "check", "vulncheck"}

   // detectGateTargets reads <workdir>/Makefile (and Makefile.precommit when
   // present — the fleet convention keeps VULNCHECK_IGNORE and vulncheck
   // there) and returns the known gate targets that are actually defined, in
   // knownGateTargets preference order. Returns nil, nil when the Makefile is
   // missing or defines none of the known targets — the caller escalates.
   func detectGateTargets(workdir string) ([]string, error)
   ```

   Behaviour:
   - Read `filepath.Join(workdir, "Makefile")`; if it does not exist return `nil, nil` (no error). Read `filepath.Join(workdir, "Makefile.precommit")` the same way when present.
   - A target `name` is defined when either file contains a line matching `^name\s*:` (target definition at column 0, optionally with prerequisites after the colon). A bare `name:` inside a recipe line (leading tab) does NOT count.
   - Return, in `knownGateTargets` order, each target that is defined. Use `os.ReadFile` for reads and the `github.com/bborbe/errors` convention (`errors.Wrapf(ctx, err, "read Makefile: %s", path)`) for read failures that are not `os.IsNotExist`.

4. **Implement `parseScannerOutput`** in `pkg/scanner_table.go`:

   ```go
   // parseScannerOutput parses one gate target's captured raw output into
   // finding rows. One row per line that carries an advisory ID; lines without
   // an ID are skipped. The three documented scanner shapes are recognized;
   // any other ID-bearing line yields a row with only the ID and a
   // target-derived scanner, so no finding is ever lost from the ground truth.
   func parseScannerOutput(target string, raw string) []ScannerFinding
   ```

   For each non-empty line: find the advisory ID via `scannerFindingIDRegexp` (first match). If none, skip the line. Otherwise build a `ScannerFinding` by shape:
   - **osv-scanner shape** — line contains ` | ` AND the literal `fixed`. Split the line on `|`; `Scanner = "osv-scanner"`, `Package = TrimSpace(fields[1])`, `FixedVersion =` the token after `"fixed"` in the last field (TrimSpace; empty when the fixed column is empty or just `"fixed"`).
   - **govulncheck shape** — line contains ` -> ` AND `@`. `Scanner = "govulncheck"`. Take the text before `" -> "`; `Package =` the module token up to the first `@` (TrimSpace, strip the version after `@`; if there is no `@`, the whole pre-arrow token); `FixedVersion =` the first whitespace token after `" -> "`.
   - **trivy shape** — line contains `│` (U+2502). Split on `│`; find the index `i` of the cell whose TrimSpace value matches the advisory ID; `Scanner = "trivy"`, `Package = TrimSpace(cells[i-1])` (empty when `i == 0`), `FixedVersion = TrimSpace(cells[i+3])` when `i+3 < len(cells)` (standard `Library | Vulnerability | Severity | Installed | Fixed | Title` layout), else empty.
   - **fallback** — none of the above shapes: `Package = ""`, `FixedVersion = ""`, `Scanner = scannerForTarget(target)`.

   Add the helper:

   ```go
   // scannerForTarget maps a gate target name to the scanner its output
   // usually carries, used only as the fallback attribution.
   func scannerForTarget(target string) string
   ```

   `vulncheck` → `"govulncheck"`, `osv-scanner` → `"osv-scanner"`, `trivy` → `"trivy"`, any other target → the target name itself.

5. **Implement the validation and rendering helpers** in `pkg/scanner_table.go`:

   ```go
   // validatePlanAgainstTable returns an error naming the first plan vuln ID
   // that does not appear verbatim in the captured scanner table. A plan whose
   // IDs are all present returns nil.
   func validatePlanAgainstTable(ctx context.Context, plan *PlanOutput, table ScannerTable) error

   // renderScannerTable renders the findings table for the planning prompt,
   // one row per line: `id | package | fixed_version | scanner`.
   func renderScannerTable(table ScannerTable) string
   ```

   `validatePlanAgainstTable`: range over `plan.Vulns`; the first `v.ID` for which `!table.Contains(v.ID)` returns `errors.Errorf(ctx, "vuln id %q not found in captured scanner output", v.ID)` (from `github.com/bborbe/errors`). The message must name the offending ID verbatim.

6. **Wire the gate into the planning step** in `pkg/steps_planning.go`:
   - Add a `gate GateRunner` field to `planningStep`.
   - Change the constructor to:

     ```go
     func NewPlanningStep(
         runner claudelib.ClaudeRunner,
         ops git.GitOps,
         gate GateRunner,
         ghToken string,
         scope InstallationScope,
     ) agentlib.Step {
         return &planningStep{runner: runner, ops: ops, gate: gate, ghToken: ghToken, scope: scope}
     }
     ```

   - In `pkg/factory/factory.go` `CreateAgent`, pass `gateRunner` into the planning step:

     ```go
     planningStep := updatepkg.NewPlanningStep(
         planningRunner,
         gitOps,
         gateRunner,
         ghToken,
         updatepkg.NewGhInstallationScope(ghToken),
     )
     ```

     `CreateAgent` already receives `gateRunner updatepkg.GateRunner`, so no other factory signature changes. This is the only production call site of `NewPlanningStep` (verify with `grep -rn 'NewPlanningStep' .`).

7. **Restructure `runInspection`** in `pkg/steps_planning.go` to return the table and to own detection + capture + validation. New signature and behaviour:

   ```go
   // runInspection detects the repo's gate targets in Go, runs each one via
   // the GateRunner, parses the raw output into the ground-truth findings
   // table, embeds the table in the planning prompt as the only source of
   // advisory IDs, runs the Claude sub-call, and validates every plan vuln ID
   // verbatim against the table. Returns (plan, table, nil) on success or
   // (nil, nil, failResult) on runner/parse/validation error.
   func (s *planningStep) runInspection(
       ctx context.Context,
       md *agentlib.Markdown,
       workdir string,
   ) (*PlanOutput, ScannerTable, *agentlib.Result)
   ```

   Steps inside, in order:
   a. `targets, err := detectGateTargets(workdir)`; on `err` return `(nil, nil, failed("detect gate targets: " + err.Error()))`.
   b. If `len(targets) == 0`, return `(nil, nil, needsInput("no gate target found in " + repo + " Makefile — add a precommit/check/vulncheck target or handle manually"))`, where `repo` is read from `md.Frontmatter` (`repo, _ := md.Frontmatter.String("repo")`). This preserves the existing message and moves the detection before any LLM call.
   c. Accumulate the table across targets:

      ```go
      table := ScannerTable{}
      for _, target := range targets {
          output, exitCode, runErr := s.gate.RunTargetFull(ctx, workdir, target)
          rows := parseScannerOutput(target, output)
          if runErr != nil && len(rows) == 0 {
              glog.V(2).Infof("planning: gate target %s failed exit=%d rows=0 output=%q", target, exitCode, output)
              return nil, nil, failed(fmt.Sprintf("gate target %q failed (exit %d) with no parseable findings: %s", target, exitCode, truncateTail(output, gateTailMaxBytes)))
          }
          table = append(table, rows...)
      }
      ```

      The `runErr != nil && len(rows) == 0` branch is the empty-on-error guard (spec failure mode "Gate target exits non-zero or scanner errors"): a scanner target that exits non-zero because it FOUND vulnerabilities produces parseable rows (that is the normal finding path and must proceed); a target that fails with no parseable rows is an error and must NOT be read as clean. Use the existing `truncateTail` and `gateTailMaxBytes` from `pkg/gate_runner.go` for the message tail.
   d. Build the prompt exactly as:

      ```go
      prompt := prompts.PlanningPrompt() +
          "\n\n## Workdir\n\n" + workdir +
          "\n\n## Target Go\n\n" + targetGoVersion() +
          "\n\n## Scanner Findings\n\nThe findings below were captured by Go from running the repo's own gate targets — they are the ONLY source of advisory IDs. Every vuln ID you report MUST appear in this table verbatim.\n\n" + renderScannerTable(table) +
          "\n\n## Task\n\n" + taskContent
      ```

      where `taskContent` is produced by `md.Marshal(ctx)` exactly as today.
   e. Run the runner and parse exactly as today (`s.runner.Run`, then `parseJSONResponse[PlanOutput](ctx, runResult.Result)`), keeping the existing `failed("claude planning run: …")` and `failed("parse planning output: …")` messages.
   f. After a successful parse, overwrite the model's gate-target claim with the Go-detected set: `plan.GateTargets = targets`.
   g. Validate before returning: `if err := validatePlanAgainstTable(ctx, plan, table); err != nil { return nil, nil, failed("plan validation: " + err.Error()) }`. On validation failure the run is `failed`, naming the fabricated ID, and the plan never reaches `parkFindings` or the `needs_input` routing.

   Update the single call site in `Run`: `plan, table, failResult := s.runInspection(ctx, md, workdir)`.

8. **Make the park escalation carry the verbatim scanner row** in `pkg/steps_planning.go`. Change the signature and body of `parkMessage`:

   ```go
   // parkMessage assembles the design-D4 park escalation: every unfixable
   // finding ID with its verbatim scanner row (id, scanner, fixed version)
   // from the captured table, plus the three suppression surfaces an
   // operator-approved suppression would touch. The model's prose reason is
   // deliberately NOT carried — the operator's suppression decision must be
   // made against the real finding.
   func parkMessage(parked []PlanVuln, table ScannerTable) string
   ```

   For each parked vuln: `row, ok := table.Row(v.ID)` (validation guarantees presence; if `!ok`, fall back to `ScannerFinding{ID: v.ID, Scanner: v.Scanner}` defensively). Build each entry as:

   ```go
   entry := row.ID + " (scanner=" + row.Scanner + ", fixed_version=" + row.FixedVersion + ")"
   ```

   Keep the existing envelope `"unfixable findings — suppress with justification or hold: %s; %s"` with `strings.Join(findings, "; ")` and `suppressionSurfacesHint`. Update the call in `Run` to `msg := parkMessage(parked, table)`.

9. **Rewrite `pkg/prompts/planning.md`** so gate-target detection and scanner running are Go-owned and the model only classifies the captured table:
   - Delete step 2 ("Detect gate targets from the Makefile — never hardcode a scanner.") entirely.
   - Delete step 5 ("Vulnerabilities — run the REPO'S OWN gate targets, not a hardcoded scanner. …") entirely.
   - In `## Context`, after the `## Task` bullet, add:

     ```
     - `## Scanner Findings` — the findings table Go captured by running the
       repo's own gate targets and parsing their output. It is the ONLY source
       of advisory IDs: every `vulns[].id` you report MUST be one of these IDs,
       copied verbatim. Never invent, guess, or modify an advisory ID, and never
       add a finding that is not listed here.
     ```

   - Renumber the remaining steps (precondition, Go directive, outdated deps, classification, has_work). Reword the classification step to: classify each finding listed in `## Scanner Findings` fix-vs-park per the existing rules (fix = a real fixed version exists and no out-of-scope major bump; park = no fixed version / major required; never plan a suppression — the agent parks the whole task naming the park findings). Add an explicit line: "Report ONLY findings listed in `## Scanner Findings`. Never add a finding ID that is not in the table."
   - Keep the `## Output` JSON shape byte-identical (contract unchanged). In the field rules, replace the `gate_targets` rule with: "`gate_targets` is detected and populated by Go from the repo's Makefile — you may omit it or set it to `[]`; Go overrides it." Keep the existing `go_bump`, `vulns`, and `action` rules.
   - After editing, the file MUST NOT contain the literal phrases `Detect gate targets` or `Run the detected scanner` anywhere (case-sensitive, spec AC2 grep), and MUST contain at least one line matching `Scanner Findings` (spec AC3 grep).

10. **Update `pkg/prompts/prompts_test.go`** — the test `"instructs the repo's own gate detection, never a hardcoded scanner"` currently asserts `ContainSubstring("gate targets")` and `ContainSubstring("never hardcode")`. Replace it with assertions that the prompt now expects Go-captured facts: `ContainSubstring("Scanner Findings")`, `ContainSubstring("ONLY source of advisory IDs")`, and `ContainSubstring("Never add a finding ID that is not in the table")`. Keep the other existing assertions (non-empty, fix/park, READ-ONLY).

11. **Rework `pkg/steps_planning_test.go`** for the fixture-workdir shape and add the new paths. The workdir is deterministic: `task_identifier: test-task-1` yields `os.TempDir()/github-update-go-test-task-1`, and `setupWorkdir` runs `os.RemoveAll(workdir)` inside `Run`, so the fixture must be created AFTER that — set `ops.CloneAtRefStub` to create the fixture workdir + files (mirroring a real clone):

    ```go
    ops.CloneAtRefStub = func(ctx context.Context, url, ref, workdir string) error {
        if err := os.MkdirAll(workdir, 0o755); err != nil {
            return err
        }
        return os.WriteFile(filepath.Join(workdir, "Makefile"), []byte(fixtureMakefile), 0o644)
    }
    ```

    Construct the step with the REAL `updatepkg.NewOSExecGateRunner()` (this is the actual seam the fix ships; the fixture Makefile uses `@echo` recipes so no scanner binary is needed), and set `runner.RunReturns(...)` per case. Cover at minimum:

    - **Fixture Makefile** defining `check:` and `vulncheck:` with `@echo` recipes emitting canned scanner output, e.g. `check` echoes `GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6` (osv shape) and `GO-2026-5932<TAB>golang.org/x/crypto/openpgp@v0.0.0-20241113183425-a8a1ce24caf7 -> v0.38.0<TAB>OpenPGP default weak` (govuln shape); `vulncheck` echoes `CVE-2026-9999<TAB>golang.org/x/net@v0.32.0 -> v0.36.0<TAB>summary` (govuln shape).
    - **Happy path** (AC 1 + AC 5): runner returns a plan with `"outcome":"ready","has_work":true,"vulns":[{"id":"GO-2026-1234","action":"fix",...}]`. Assert: `Run` → `AgentStatusDone`, `NextPhase == "execution"`; the plan section round-trips; `plan.GateTargets == []string{"check","vulncheck"}` (Go-detected, overriding the model); the prompt passed to `runner.Run` (capture via `runner.RunArgsForCall(0)` or `RunStub`) contains `## Scanner Findings` and the rendered table row `GO-2026-1234 | stdlib | 1.26.6 | osv-scanner` verbatim (the rendered row format is `id | package | fixed_version | scanner`; the osv fixture's `fixed_version` is the token after "fixed" in the last field — `1.26.6`, NOT the installed-version column `1.26.5`).
    - **Park path** (AC 6): runner returns `"vulns":[{"id":"GO-2026-5932","action":"park",...},{"id":"CVE-2026-9999","action":"park",...}]`. Assert `needsInput` message contains the raw table rows: `GO-2026-5932 (scanner=govulncheck, fixed_version=v0.38.0)` and `CVE-2026-9999 (scanner=govulncheck, fixed_version=v0.36.0)` (id + scanner + fixed-version column copied from the parsed table), plus `VULNCHECK_IGNORE`, `.osv-scanner.toml`, `.trivyignore`, and that the model's prose reason does NOT appear.
    - **Fabricated-ID rejection** (AC 4, fully-absent case): runner returns a plan with `"vulns":[{"id":"GO-2025-3283","action":"fix",...}]`. Assert `AgentStatusFailed`, message contains `GO-2025-3283`, and the run never parks and never emits `needs_input` (assert status is NOT `AgentStatusNeedsInput` and `ops`-independent — assert via the returned status only).
    - **Prefix-collision rejection** (AC 4): add a fixture line `GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6` to `check`; runner returns a plan with `"vulns":[{"id":"GO-2026-50260","action":"fix",...}]`. Assert `AgentStatusFailed` and message contains `GO-2026-50260` — the prefix-shared ID must NOT validate.
    - **no_update_needed** (AC 5 + Desired Behavior 8): gate output empty, exit 0; runner returns `"outcome":"no_update_needed","has_work":false`. Assert `AgentStatusDone`, `NextPhase == "done"`.
    - **Empty-on-error is not clean** (failure-mode row "Gate target exits non-zero"): fixture target recipe `@echo 'make: something broken' >&2; exit 1`; assert `AgentStatusFailed`, message contains the failing target name and exit code, and NOT `needs_input`.
    - **No gate target**: `CloneAtRefStub` writes no Makefile; assert `needsInput` with `ContainSubstring("no gate target found")` and that `runner.RunCallCount() == 0` (detection happens before any LLM call).

12. **Create `pkg/scanner_table_test.go`** in `package pkg_test` (external, joining the existing `pkg/pkg_suite_test.go` suite) covering the parser and validator directly:
    - `parseScannerOutput` for each of the three shapes (osv, govuln, trivy `│` row), asserting ID/Package/FixedVersion/Scanner per row; empty output → empty table; a line with no advisory ID → skipped; a line with an ID in an unknown shape → fallback row with ID + target-derived scanner and empty package/fixed.
    - ID full-capture: a line containing `GO-2026-50260` yields the row ID exactly `GO-2026-50260`, never `GO-2026-5026`.
    - `detectGateTargets` against real fixture Makefiles written to a temp dir (via `t.TempDir()`/Ginkgo temp dir): defines `check`+`vulncheck` → `["check","vulncheck"]`; defines only `precommit` → `["precommit"]`; defines a known target only inside a recipe line (leading tab) → not detected; defines none of the known targets → nil; missing Makefile → nil.
    - `validatePlanAgainstTable`: table `{GO-2026-1234}` + plan ID `GO-2026-1234` → nil; plan ID `GO-2025-3283` → error whose message contains `GO-2025-3283`; table `{GO-2026-5026}` + plan ID `GO-2026-50260` → error naming `GO-2026-50260` (exact match, not prefix).
    - `ScannerTable.Contains`/`Row` exact-match semantics (a lookup for `GO-2026-50260` does not match row `GO-2026-5026`).
    - `renderScannerTable` renders the `id | package | fixed_version | scanner` lines for a small table.
    - `parkMessage` with a table: message contains the raw row `ID (scanner=X, fixed_version=Y)` and the three suppression surfaces.

13. **Test `GateRunner.RunTargetFull`** in `pkg/gate_runner_test.go` (create it if it does not exist; check first): a fixture Makefile whose recipe emits more than `gateTailMaxBytes` (2000) bytes → `RunTargetFull` returns the full output (length > 2000, no `...[truncated]` marker) while `RunTarget` returns the truncated tail; a target name that fails `gateTargetRegexp` (e.g. `"evil;rm -rf"`) → invalid-target error and no `make` invocation.

14. **Ensure a `## Unreleased` section exists** at the top of `CHANGELOG.md` (the current top section is `## v0.5.0` — create the `## Unreleased` header if absent, per the changelog guide) and append one bullet to it (do not replace the section, do not add a version header):

    `- fix: planning captures and parses the repo's own gate scanner output in Go, validates every plan advisory ID verbatim against the captured table, and carries the verbatim scanner row on park escalations`
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The agent must remain incapable of writing ignore entries; parking stays the only agent-side move on an unfixable finding (design D4, `suppressionSurfacesHint`). Consult `docs/design.md` decisions D1/D4 and the §4.4 state-passing invariants before implementation.
- Gate targets must still be validated against `gateTargetRegexp` before reaching `make` argv (no injection regression) — both `RunTarget` and the new `RunTargetFull` guard.
- `## Plan` output contract (`PlanOutput`) and the `## Plan` section format are UNCHANGED — the model still emits the same JSON shape; only the source of the IDs and the validation around it change. The rendered `## Scanner Findings` section is an appended context section, not part of the JSON contract.
- The planning prompt keeps its read-only discipline (no Edit/Write, no git/gh side effects). The Go side runs the repo's gate targets in the throwaway workdir clone only.
- Error handling follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf` in new code paths (the existing `fmt.Sprintf` uses in message assembly are fine), no `context.Background()` in `pkg/` non-test code.
- No new module dependencies. The parser uses only stdlib (`regexp`, `os`, `path/filepath`, `strings`) plus existing imports.
- Never hand-edit generated mocks: `mocks/gate_runner.go` is regenerated by `make generate` (part of `make precommit`).
- Existing tests must still pass; new code needs ≥80% statement coverage (`/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`).
- Do NOT touch `pkg/steps_execution.go`, `pkg/steps_review.go`, or `pkg/claude_prober.go` — the `GateRunner` interface gains a method but existing callers keep using `RunTarget` unchanged; the regenerated mock satisfies both.
- Do NOT change `main.go`, `cmd/run-task/main.go`, or the `pkg/factory/factory_test.go` `CreateAgent` calls — `CreateAgent` already receives `gateRunner` and its signature is unchanged.
</constraints>

<verification>
Run `make test` — all tests pass, including the new `pkg/scanner_table_test.go` and reworked `pkg/steps_planning_test.go` cases.

Run `make precommit` — must exit 0 (this also regenerates `mocks/gate_runner.go`).

```bash
grep -n 'Detect gate targets\|Run the detected scanner' pkg/prompts/planning.md
# expect: 0 lines (spec AC2)

grep -n 'Scanner Findings' pkg/prompts/planning.md
# expect: >= 1 line (spec AC3)

grep -rn 'RunTargetFull' pkg/gate_runner.go mocks/gate_runner.go
# expect: the interface method, the osExecGateRunner implementation, and the regenerated mock

grep -rn 'NewPlanningStep' pkg/ cmd/ main.go
# expect: pkg/steps_planning.go (definition), pkg/factory/factory.go (one call), tests only elsewhere

go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'scanner_table|gate_runner|steps_planning|total'
# expect: the new scanner_table functions at or near 100%; steps_planning runInspection/validate covered
```
</verification>
