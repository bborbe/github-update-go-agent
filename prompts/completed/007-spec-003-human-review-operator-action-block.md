---
status: completed
spec: [003-human-review-operator-action-block]
summary: 'Added plain-text ## Your Move operator-action block (PR link + merge action + change summary) written above ## Plan on human_review routing, with idempotent replace-in-place, tests, changelog and design-doc updates'
execution_id: github-update-go-agent-operator-handoff-exec-007-spec-003-human-review-operator-action-block
dark-factory-version: dev
created: "2026-08-18T19:50:57Z"
queued: "2026-08-18T20:00:56Z"
started: "2026-08-18T20:00:56Z"
completed: "2026-08-18T20:14:30Z"
branch: dark-factory/human-review-operator-action-block
---

# Operator-action block on human_review handoff

<summary>
- Tasks that reach `human_review` now open with a `## Your Move` block at the very top of the body, above `## Plan`.
- The block names the PR as a clickable markdown link — the same URL the `## Result` JSON reports — so the operator can jump straight to it.
- The block tells the operator what to do in plain words: merge the PR.
- The block summarises what the change consists of in plain text: the Go version bump when one was recorded, otherwise the dependency-update count and the fixed-vulnerability IDs. No JSON anywhere in the block.
- When neither a version bump nor dependency/vulnerability updates were recorded, the block still appears but says so explicitly, keeping the link and the action.
- Re-running the review on an already-approved task replaces the block in place — it never duplicates it.
- Failed and `needs_input` outcomes produce no block at all.
- The existing `## Plan` / `## Result` / `## Review` JSON sections and their round-trips are untouched.
- The PR URL is validated as an http(s) URL before it is rendered; anything else shows a "PR URL unavailable" placeholder rather than a malformed link.
- The merge decision stays with the human — the agent gains no merge capability.
</summary>

<objective>
Make every task that routes to `human_review` open with a plain-text `## Your Move` operator-action block — a clickable PR link, the imperative merge action, and a plain-language summary of what changed — positioned above `## Plan`, idempotent on re-run, and absent on `Failed`/`needs_input`, without altering any existing JSON section.
</objective>

<context>
Read the injected container `CLAUDE.md` (`/home/node/.claude/CLAUDE.md`) for project conventions — there is no repo-root CLAUDE.md.

Read fully before changing anything:
- `pkg/steps_review.go` — `reviewStep.Run` (it already extracts `result` and `plan` at the top, computes `output` from the `approved := …` expression, and returns `s.finish(ctx, md, output)`), the `Run` doc comment's step 6, `NewReviewStep`, and `checkPR`. This prompt edits `Run` only — the checks, the `approved` expression and `finish` are otherwise untouched.
- `pkg/result_output.go` — `ResultOutput.PRURL` (json `pr_url`), `ResultOutput.DepsUpdated` (json `deps_updated`), `ResultOutput.VulnsFixed []string` (json `vulns_fixed`).
- `pkg/plan_output.go` — `PlanOutput.GoBump *GoBump` (json `go_bump`), `GoBump{From, To string}` (json `from`/`to`).
- `pkg/review_output.go` — the BSD copyright header to copy for the new file, and `ReviewOutput` (read for orientation; do NOT change it).
- `pkg/steps_review_test.go` — the Ginkgo suite: the shared `BeforeEach` happy-path fakes, the `reviewTaskMD` / `changelogMaster` / `changelogBranch` constants (note the `"` + "```json" + `"` fence concatenation), and the `Describe` blocks including `"PR not open"`. New fixtures are copies of `reviewTaskMD`.
- `pkg/export_test.go` — the in-repo test-export pattern (`package pkg` internal file exposing unexported identifiers via `var`, e.g. `NormalizeCloneURLToHTTPS = normalizeCloneURLToHTTPS`).
- The agent-lib markdown API you will call, at `$(go env GOMODCACHE)/github.com/bborbe/agent@v0.81.3/agent_markdown.go`:
  - `type Section struct { Heading string; Body string }`
  - `func (m *Markdown) FindSection(heading string) (*Section, bool)` — mutating the returned section's fields updates the Markdown in place.
  - `func (m *Markdown) InsertSection(pos int, section Section)` — out-of-range positions clamp to `[0, len(Sections)]`.
  - `func (m *Markdown) Marshal(ctx context.Context) (string, error)` — renders each section as `<heading>\n\n<body>\n`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage ≥80%.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-doc-best-practices.md` — GoDoc comments start with the name, describe behavior not implementation.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits: `funlen` (80 lines / 50 statements), `golines` 100.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and entry style.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.

Run this first to confirm the call site and the section machinery you are building on:

```bash
grep -n 'ExtractSection\|s.finish\|approved :=' pkg/steps_review.go
grep -n 'func (m \*Markdown) FindSection\|func (m \*Markdown) InsertSection' "$(go env GOMODCACHE)/github.com/bborbe/agent@v0.81.3/agent_markdown.go"
```
</context>

<requirements>
1. **Create `pkg/your_move.go`** in `package pkg`, starting with the 3-line BSD copyright header copied verbatim from `pkg/review_output.go`. It holds the plain-text operator-action block builder and the idempotent section writer. Imports: `net/url`, `strconv`, `strings`, and `agentlib "github.com/bborbe/agent"`. No new module dependencies (all stdlib).

   ```go
   // yourMoveHeading is the fixed heading of the operator-action block a task
   // routed to human_review opens with (named in the spec verification:
   // grep -n "^## Your Move").
   const yourMoveHeading = "## Your Move"

   // isValidPRURL reports whether raw is an absolute http(s) URL — the only
   // shape safe to interpolate into the block as a clickable markdown link.
   func isValidPRURL(raw string) bool {
   	u, err := url.Parse(raw)
   	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
   }
   ```

   `buildYourMoveBody` renders the block. It interpolates ONLY the agent's own `ResultOutput.PRURL` (validated above) plus numeric/ID fields — never the PR body, description, or any repo-file content, and no HTML:

   ```go
   // buildYourMoveBody renders the plain-text operator-action block for a
   // human_review handoff: a clickable PR link, the single action the operator
   // owns (merge), and what the change consists of. No JSON — the
   // machine-readable contract stays in ## Plan / ## Result / ## Review. An
   // empty or non-http(s) PRURL renders the "PR URL unavailable" placeholder
   // (the ## Result JSON still carries the branch name); the Go version bump
   // is shown when go_bump is present, otherwise the dependency-update count
   // and the fixed-vulnerability IDs, and an explicit "No version bump
   // recorded" line when none were recorded.
   func buildYourMoveBody(result *ResultOutput, plan *PlanOutput) string {
   	lines := []string{}
   	if isValidPRURL(result.PRURL) {
   		lines = append(lines, "[Open the PR]("+result.PRURL+")")
   	} else {
   		lines = append(lines, "PR URL unavailable")
   	}
   	lines = append(lines, "**Merge the PR** to apply the update.")

   	if plan.GoBump != nil {
   		lines = append(lines, "Go version bump: "+plan.GoBump.From+" → "+plan.GoBump.To)
   	} else {
   		if result.DepsUpdated > 0 {
   			lines = append(lines, "Updated "+strconv.Itoa(result.DepsUpdated)+" dependencies")
   		}
   		if len(result.VulnsFixed) > 0 {
   			lines = append(lines, "Fixed vulnerabilities: "+strings.Join(result.VulnsFixed, ", "))
   		}
   		if result.DepsUpdated == 0 && len(result.VulnsFixed) == 0 {
   			lines = append(lines, "No version bump recorded")
   		}
   	}
   	return strings.Join(lines, "\n\n")
   }
   ```

   `writeYourMoveSection` places the block ABOVE `## Plan` on first write, and replaces in place on a re-run (replace-not-append — a re-triggered review must never leave a second block):

   ```go
   // writeYourMoveSection inserts the ## Your Move block immediately above
   // ## Plan, or updates it in place on a re-run so a re-triggered review
   // never duplicates it. Only called when the review routes to human_review.
   func writeYourMoveSection(md *agentlib.Markdown, result *ResultOutput, plan *PlanOutput) {
   	section := agentlib.Section{Heading: yourMoveHeading, Body: buildYourMoveBody(result, plan)}
   	if existing, ok := md.FindSection(yourMoveHeading); ok {
   		existing.Body = section.Body
   		return
   	}
   	pos := len(md.Sections)
   	for i, s := range md.Sections {
   		if s.Heading == "## Plan" {
   			pos = i
   			break
   		}
   	}
   	md.InsertSection(pos, section)
   }
   ```

   Do NOT use `agentlib.MarshalSectionTyped` for this section — that renders a JSON-fenced body, and the spec requires the block to contain no JSON (spec AC5).

2. **Wire the block into `Run`** in `pkg/steps_review.go`. Immediately after the `output := ReviewOutput{ … }` literal and before `return s.finish(ctx, md, output)`, insert:

   ```go
   if output.Approved {
   	writeYourMoveSection(md, result, plan)
   }
   ```

   `result` and `plan` are the pointers `Run` already extracted at its top — no re-extraction. This is the ONLY call site: the block is written exclusively when the review approves, which is the only path that routes `human_review` (`finish` maps `Approved` → `Done`/`NextPhase human_review`). The early `finish` return when `result.PRURL == "" || result.Branch == ""` and every rejected `finish` return stay untouched and therefore never write a block (spec non-goal: no block on `Failed`/`needs_input`; spec AC6).

   Update the `Run` doc comment's step 6 so it reads that approval also writes the `## Your Move` operator-action block, e.g.:

   ```
   //  6. All true → ## Review approved + ## Your Move operator-action block +
   //     Done/NextPhase human_review (the ONLY writer of that phase; success
   //     semantics per doctrine). Any false → ## Review approved:false +
   //     Status Failed, NO NextPhase, NO ## Your Move block.
   ```

   Also extend the `NewReviewStep` doc comment by one sentence: on approval the step writes the plain-text `## Your Move` operator-action block (PR link + merge action + change summary) above `## Plan` for the human operator.

   Do NOT change `checkPR`, the `approved := …` expression, the `finish` function, `ReviewChecks`, `ReviewOutput`, `PlanOutput`, `ResultOutput`, or any `json:` tag. Do NOT touch `main.go`, `cmd/run-task/main.go`, `pkg/factory/factory.go`, or the mocks (no interface signature changes).

3. **Add the test-only export** in `pkg/export_test.go`. Add one line to the existing `var (` block (no new imports needed for this line — the file's existing imports are unchanged):

   ```go
   BuildYourMoveBody = buildYourMoveBody
   ```

4. **Add review-step tests to `pkg/steps_review_test.go`.** Add `"strings"` to the file's imports. Keep the existing suite and the shared happy-path fakes; add the blocks below.

   - New `Describe("Your Move operator-action block", …)` using the existing shared fixture `reviewTaskMD` (which has no `go_bump`, no `deps_updated`, no `vulns_fixed` — it therefore doubles as the "nothing recorded" edge case):
     - One `It` running `step.Run(ctx, md)` (expect `err` nil) then asserting: `section, ok := md.FindSection("## Your Move")` is `true`; `section.Body` contains `[Open the PR](https://github.com/bborbe/demo/pull/42)` (the fixture's `pr_url` — spec AC2), contains `**Merge the PR** to apply the update.` (spec AC3), contains `No version bump recorded` (spec Failure-Modes edge row), and does NOT contain `{` (spec AC5). Assert ordering at the section level: `md.Sections[0].Heading` equals `"## Your Move"` and `md.Sections[1].Heading` equals `"## Plan"`.
     - One `It` encoding the golden-body grep (spec Verification): run once, `body, err := md.Marshal(ctx)`, split on `"\n"`, find the index of the line `"## Your Move"` and of `"## Plan"`, assert both `>= 0` and that the `## Your Move` index is strictly smaller than the `## Plan` index (spec AC1).
     - One `It` for idempotency (spec AC7): run `step.Run(ctx, md)` twice; count sections with `Heading == "## Your Move"` and assert the count is exactly 1.
   - New `It` inside the existing `Describe("PR not open", …)`: after `step.Run`, assert `md.FindSection("## Your Move")` returns `ok == false` (spec AC6 — a rejected body carries no block).
   - Add a constant `reviewTaskMDGoBump` — a copy of `reviewTaskMD` with `task_identifier` changed to `test-task-2` and the Plan JSON gaining `"go_bump": {"from": "1.26.4", "to": "1.26.6"}` as the first field after `"has_work": true,` (keep the `"` + "```json" + `"` fence concatenation exactly as `reviewTaskMD` does). New `Describe("Your Move block with a Go version bump", …)` with a nested `BeforeEach` re-parsing `md` from `reviewTaskMDGoBump`; one `It` asserting `section.Body` contains `Go version bump: 1.26.4 → 1.26.6` — which proves both `go_bump.from` and `go_bump.to` are interpolated (spec AC4).
   - Add a constant `reviewTaskMDVuln` — a copy of `reviewTaskMD` with `task_identifier` changed to `test-task-3` and the Result JSON gaining `"deps_updated": 3,` and `"vulns_fixed": ["GO-2024-1234", "CVE-2025-1000"],` (order as shown, before `"gate_exit"`). New `Describe("Your Move block with dependency and vulnerability updates", …)` with a nested `BeforeEach` re-parsing `md` from `reviewTaskMDVuln`; one `It` asserting `section.Body` contains `Updated 3 dependencies` AND `Fixed vulnerabilities: GO-2024-1234, CVE-2025-1000` (spec AC4).

5. **Create `pkg/your_move_test.go`** in `package pkg_test` (same BSD header, imports: ginkgo, gomega, `pkg "github.com/bborbe/github-update-go-agent/pkg"` — NO `"context"` import: `BuildYourMoveBody` is a pure function taking no context, unlike the review-step tests in step 4 which do use `context`). Add a top-level `var _ = Describe("YourMoveBody", …)` driving the exported builder directly across its boundaries:
   - Empty PRURL: `pkg.BuildYourMoveBody(&pkg.ResultOutput{}, &pkg.PlanOutput{})` contains `PR URL unavailable`, `**Merge the PR** to apply the update.` and `No version bump recorded`, and does NOT contain `[` (no malformed link on the placeholder — the `PR URL unavailable` placeholder failure-mode row, tested at the builder level because the review step fails closed before `Run` could ever approve with an empty PRURL).
   - Non-http(s) PRURL: `pkg.BuildYourMoveBody(&pkg.ResultOutput{PRURL: "javascript:alert(1)"}, &pkg.PlanOutput{})` contains `PR URL unavailable` (URL-validation boundary, spec Security).
   - Valid URL: `pkg.BuildYourMoveBody(&pkg.ResultOutput{PRURL: "https://github.com/bborbe/demo/pull/7"}, &pkg.PlanOutput{})` contains `[Open the PR](https://github.com/bborbe/demo/pull/7)` and `No version bump recorded`.
   - Go bump: `&pkg.PlanOutput{GoBump: &pkg.GoBump{From: "1.26.4", To: "1.26.6"}}` → contains `Go version bump: 1.26.4 → 1.26.6`.
   - Deps + vulns: `&pkg.ResultOutput{PRURL: "https://github.com/bborbe/demo/pull/7", DepsUpdated: 3, VulnsFixed: []string{"GO-2024-1234", "CVE-2025-1000"}}` with an empty `PlanOutput{}` → contains `Updated 3 dependencies` and `Fixed vulnerabilities: GO-2024-1234, CVE-2025-1000`.
   - No JSON anywhere: every builder result asserted in these cases must NOT contain `{` (spec AC5 at the builder level too).

6. **Changelog.** `CHANGELOG.md` currently has no `## Unreleased` section (highest is `## v0.6.0`). Create `## Unreleased` immediately after the preamble block and directly above `## v0.6.0` (per the changelog guide — never inside or above the preamble), then append this single bullet:

   `- feat: ai_review writes a ## Your Move operator-action block at the top of a human_review-routed task body — a clickable PR link, the merge action, and a plain-text change summary (Go version bump and/or dependency/vulnerability updates) — so the operator can act without reading the ## Plan / ## Result / ## Review JSON`

7. **Design doc.** `docs/design.md` § 4.3 (`ai_review` table) and its section list state that body sections are typed JSON via `agentlib.MarshalSectionTyped` / `ExtractSection[T]`. The new plain-text `## Your Move` block is a deliberate exception. Amend `docs/design.md` with one line noting that a `human_review`-routed task additionally opens with a plain-text `## Your Move` operator-action block (written via `FindSection`/`InsertSection`, not `MarshalSectionTyped`) so the doc does not become stale.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The `## Plan` / `## Result` / `## Review` JSON sections keep their exact current shape; the new section must not break `agentlib.ExtractSection[PlanOutput|ResultOutput|ReviewOutput]` round-trips. Do NOT modify `PlanOutput`, `ResultOutput`, `ReviewOutput`, `ReviewChecks`, or any `json:` tag.
- The `## Your Move` heading is fixed (named in the spec's Verification section) — the block body is plain text with a single markdown link: no JSON, no HTML, no `{` between the heading and the next `## ` heading.
- The PR URL is sourced from the agent's own `ResultOutput.PRURL`, validated as an http(s) URL before rendering. The block never interpolates the PR body, description, or any repo-file content.
- The block is written only when the result routes to `human_review` (i.e. only when `output.Approved` is true). No block on `Failed` / `needs_input`.
- Do NOT change the `ai_review` checks, the `PR_TARGET` handling, the draft→ready ordering, or the approve/reject decision logic. The merge decision stays with the human — do NOT add any `gh pr merge` or ready-flip capability.
- Re-running the review step on an already-human_review body must replace the existing block, never append a second (replace-not-append semantics).
- No new module dependencies — the new code uses stdlib (`net/url`, `strconv`, `strings`) plus the already-required `github.com/bborbe/agent`.
- Never `fmt.Errorf`; use `github.com/bborbe/errors` (the new functions have no error paths, so none is needed). Never `context.Background()` in non-test `pkg/` code.
- Do NOT run `go mod vendor` and do NOT use `-mod=vendor`; this repo does not commit `vendor/` (use `-mod=mod`).
- Keep functions inside the `funlen` limit (80 lines / 50 statements) and lines under 100 chars (`golines`).
- No mock regeneration is expected — no interface signature changes. If `make generate` is run it must produce no diff in `mocks/`.
- Existing tests must still pass; new code needs ≥80% statement coverage.
</constraints>

<verification>
Run `make test` — all tests pass, including the new `Your Move` and `YourMoveBody` cases.

Run `make precommit` — must exit 0.

```bash
grep -n 'writeYourMoveSection' pkg/steps_review.go
# expect: exactly 1 line — the approved-gated call in Run

grep -rn 'MarshalSectionTyped' pkg/your_move.go
# expect: 0 lines — the block is plain text, not a typed JSON section (spec AC5)

grep -rn '## Your Move' pkg/*.go
# expect: the yourMoveHeading constant in pkg/your_move.go plus the test assertions

grep -n 'Go version bump\|Merge the PR\|PR URL unavailable\|No version bump recorded' pkg/your_move.go
# expect: >= 4 lines — link/action/placeholder/edge lines are all rendered in plain text

grep -n 'go_bump' pkg/steps_review_test.go
# expect: >= 1 line — the go-bump fixture proves from/to interpolation (spec AC4)

go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'your_move|steps_review|total'
# expect: your_move functions at or near 100%; the steps_review total not lower than before
```
</verification>
