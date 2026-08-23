---
status: approved
spec: [005-bug-ai-review-leaked-tag-and-merged-false-positive]
created: "2026-08-24T09:35:00Z"
queued: "2026-08-23T23:30:12Z"
branch: dark-factory/bug-ai-review-leaked-tag-and-merged-false-positive
---

<!-- NOTE FOR THE AUDITOR (not part of the executable task):
This prompt is the second of two for spec 005 and is intended to execute after
prompt `spec-005-check-no-new-tag-base-reachable` (the filenames sort in that
order). The checkPR code change and its AC3/AC4 unit tests are independent of
the sibling; only the full-pipeline repro test and the docs/design.md wording
describe the combined post-fix ai_review behavior. No scenario prompt is
emitted: the scenario-writing four-condition test fails on condition (a) —
unit tests in pkg/steps_review_test.go reach both fixed behaviors directly
(MERGED verdict via the gh fake; base-reachable tag exclusion via the RevList
base argument), and the spec's operator ladder (Rung-2 repro replay on dev)
already covers the real-cluster e2e. The spec's "release + deploy" third
prompt is operator-only and stays on the spec's Verification ladder, not here.
-->

# Accept an already-MERGED update PR as shipped in ai_review

<summary>
- The ai_review step now treats an already-MERGED pull request as the shipped success state instead of rejecting it with "pr state is MERGED, expected OPEN".
- A merged PR routes through the existing approved path — the review verdict is approved and the task moves to human_review for the operator to close — so no failed publish re-files the task and the fleet slot stops cycling.
- A genuinely surprising non-shipped PR state (e.g. CLOSED) still produces the existing "pr state is X, expected OPEN" rejection — the fail-closed contract for real anomalies is unchanged.
- The serialized pr_open check keeps its meaning (a merged PR is not open, so it reports false); the acceptance decision lives in the review verdict, not in the pr_open key.
- The draft-ness check is bypassed for a merged PR — a merged PR is never draft, so requiring draft-ness would wrongly reject a completed task.
- The design doc now records both corrected behaviors: base-reachable release tags are not leaks, and a MERGED PR is accepted as shipped.
- A full-pipeline unit test replays the production repro shape (release tag in history + already-merged PR) and asserts an approved verdict with no status=failed re-file.
</summary>

<objective>
Make the ai_review step accept a pull request that has already been MERGED as the shipped success state — no "expected OPEN" rejection, verdict approved, routing to human_review so the operator closes the task and no `status=failed` re-file fires — while still rejecting genuinely surprising non-shipped states (e.g. CLOSED), and record both corrected behaviors in the design doc.
</objective>

<context>
Read the injected container `CLAUDE.md` (`/home/node/.claude/CLAUDE.md`) for project conventions — there is no repo-root CLAUDE.md.

Read fully before changing anything:
- `pkg/steps_review.go` — the whole file: `reviewStep.Run` (note the `draftMatches := s.checkPR(...)` call and the `approved := checks.PROpen && draftMatches && ...` aggregation), `checkPR` (the current `checks.PROpen = state == "OPEN"` + unconditional "pr state is X, expected OPEN" note), `finish` (approved → `AgentStatusDone` + `NextPhase: human_review`; rejected → `AgentStatusFailed` with NO NextPhase), and `checkNoNewTag` (already fixed by the sibling prompt to use `origin/master` — do not modify it here).
- `pkg/review_output.go` — the `ReviewChecks` struct and the serialized `pr_open` / `pr_draft` keys. `pr_open` must KEEP its meaning ("is the PR open") — a MERGED PR reports false; the acceptance decision lives in the review verdict, not the key.
- `pkg/gh_cli.go` — `GhCli.ViewPR(ctx, prURL) (state string, isDraft bool, err error)` and its doc ("returns the state (e.g. 'OPEN', 'MERGED', 'CLOSED')"). The seam is UNCHANGED.
- `pkg/steps_review_test.go` — the Ginkgo `Describe("ReviewStep", ...)` suite: the outer `BeforeEach` fakes (`gh.ViewPRReturns("OPEN", true, nil)`, compliant CHANGELOG via `ops.CloneAtRefStub`, `ops.RevListReturns([]string{"deadbeef1","deadbeef2"}, nil)`, `ops.LsRemoteTagsReturns([]string{"1111111","2222222"}, nil)`), the existing `Describe("PR not open", ...)` (CLOSED row — keep and extend), the existing `Describe("tag leaked onto a branch commit", ...)`, and the `reviewTaskMD` fixture (pr_url pull/42, branch fix/update-go-6d1f27f).
- The spec `specs/in-progress/005-bug-ai-review-leaked-tag-and-merged-false-positive.md` — the Goal, Desired Behaviors 4–6, Failure Modes row 4 (PR OPEN→MERGED between ViewPR and review write — MERGED accepted, no race window), and Acceptance Criteria AC3–AC5.
- `docs/design.md` § 4.3 ai_review table ("Side effects" row), § 4.4 (State passing + invariants paragraph), and § 8.1 (ai_review acceptance row) — these are the three passages this prompt updates.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` usage; never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage ≥80%.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits: `funlen` (80 lines / 50 statements), `golines` 100.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and entry style.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.

Run this first to confirm the exact current shapes:

```bash
grep -n 'checkPR\|checks.PROpen\|draftMatches\|approved :=' pkg/steps_review.go
grep -n 'PR not open\|ViewPRReturns\|expected OPEN\|tag leaked' pkg/steps_review_test.go
grep -n 'gh pr view --json state,isDraft\|no tag at branch' docs/design.md
```
</context>

<requirements>
1. **Accept a MERGED PR as the shipped success state.** In `pkg/steps_review.go`, modify `checkPR` so that `state == "MERGED"` is accepted without notes — it is the shipped success state (the operator merged the PR; the task is complete). The exact contract:
   - Keep `checks.PRDraft = isDraft` reporting the RAW observed draft-ness for every successful `ViewPR` (including MERGED) — the serialized `pr_draft` key keeps its meaning.
   - For `state == "MERGED"`, return `true` (accepted) immediately WITHOUT setting `checks.PROpen` (it stays false — a merged PR is not open; the serialized `pr_open` key keeps its "is the PR open" meaning) and WITHOUT appending the "expected OPEN" or "draft-ness mismatch" notes (a merged PR is never draft, so the draft check must not reject a completed task). Spec Desired Behavior 4 + Failure Modes row 4 (no race window: OPEN→MERGED between `ViewPR` and the review write simply accepts as shipped).
   - For every other state the existing behavior is unchanged: `checks.PROpen = state == "OPEN"`; when not open, append `"pr state is "+state+", expected OPEN"` (this still fires for genuinely surprising non-shipped states like CLOSED — spec AC4 / Desired Behavior 5); the draft-match computation and its mismatch note stay exactly as they are.
   - Resulting shape (the only added branch is the MERGED early return):
     ```go
     checks.PRDraft = isDraft
     if state == "MERGED" {
     	// MERGED is the shipped success state — the operator already merged
     	// the PR. Accept without notes so the review routes approved →
     	// human_review and the task is closed, not re-filed. No draft-ness
     	// check: a merged PR is never draft, and checks.PROpen stays false
     	// (raw — it is not open).
     	return true
     }
     checks.PROpen = state == "OPEN"
     if !checks.PROpen {
     	*notes = append(*notes, "pr state is "+state+", expected OPEN")
     }
     ```
     For the non-MERGED path, the return MUST fail closed: `return checks.PROpen && draftMatches` (NOT the bare existing `return draftMatches`). Without this, a genuinely surprising non-shipped state (e.g. CLOSED) whose draft-ness happens to match the target returns true and is wrongly approved — regressing spec AC4 / Desired Behavior 5 and the fail-closed contract. Keep the draft-mismatch note for the OPEN path verbatim; the non-OPEN non-MERGED return is `false`.

2. **Use the checkPR verdict in the approval aggregation.** In `reviewStep.Run`, `checkPR`'s return value now means "the PR state is acceptable AND draft-ness matches" (or the PR is MERGED/shipped). Change the aggregation so the approval uses that verdict directly instead of `checks.PROpen`:
   ```go
   prAccepted := s.checkPR(ctx, result, &checks, &notes)
   ...
   approved := prAccepted && checks.GateGreen && checks.VulnsClear &&
   	checks.ChangelogUnreleased && checks.NoNewTag
   ```
   NOTE: `checks := ReviewChecks{}` already exists at the top of `Run` (line 98) — do NOT redeclare it. Only the `draftMatches := s.checkPR(...)` line (now `prAccepted := s.checkPR(...)`) and the `approved :=` aggregation line change.
   `checks.PROpen` is still written by `checkPR` for the serialized key, but it no longer gates approval for a MERGED PR (where it is legitimately false). Do not change any other gate in the aggregation.

3. **Update the `checkPR` doc comment** so it states the MERGED-acceptance contract — keep the existing text about `pr_draft`/raw draft-ness and the fail-closed draft mismatch, and add a sentence: a MERGED PR is the shipped success state and is accepted without notes (the review routes approved → human_review so the operator closes the task); only non-shipped states (e.g. CLOSED) surface "pr state is X, expected OPEN" and fail the review. Keep it inside `funlen`/`golines` limits.

4. **Unit tests** in `pkg/steps_review_test.go` (Ginkgo, inside the existing `Describe("ReviewStep", ...)` suite):
   - **AC3 — MERGED is approved, not rejected.** Add a new `Describe("PR already merged", ...)` with `BeforeEach(func() { gh.ViewPRReturns("MERGED", false, nil) })` and an `It` asserting: `result.Status == agentlib.AgentStatusDone` (the no-`status=failed` outcome), `result.NextPhase == "human_review"`, `review.Approved` true, `review.Checks.PROpen` false (raw — the key keeps its meaning), `review.Checks.PRDraft` false, `review.Notes` does NOT contain `"expected OPEN"` and does NOT contain `"draft-ness mismatch"`.
   - **Draft check bypassed for shipped state (design decision lock).** A second `It` in the same `Describe` overriding `gh.ViewPRReturns("MERGED", true, nil)` (defensive: even if gh reported a merged PR as draft) asserting `result.Status == agentlib.AgentStatusDone`, `review.Approved` true, and no `"draft-ness mismatch"` note — proving the MERGED early return skips the draft comparison.
   - **AC4 — CLOSED still surfaces.** In the existing `Describe("PR not open", ...)` (which already sets `gh.ViewPRReturns("CLOSED", true, nil)`), extend the existing `It("rejects: approved false + Failed + NO NextPhase", ...)` with one assertion: `Expect(review.Notes).To(ContainSubstring("pr state is CLOSED, expected OPEN"))`. Do not change its rejection assertions.
   - **AC5 — full-pipeline repro replay (unit level).** Add a new `Describe("repro: release-tag-in-history + already-merged PR (spec 005)", ...)` that drives the whole `step.Run` pipeline against the production repro shape — a base-reachable release tag on master history AND an already-merged PR:
     ```go
     BeforeEach(func() {
     	gh.ViewPRReturns("MERGED", false, nil)
     	ops.RevListReturns([]string{"f8b922c2"}, nil)      // the branch's own commit
     	ops.LsRemoteTagsReturns([]string{"6e16a948"}, nil) // legitimate release tag on master
     })
     ```
     with an `It` asserting: `result.Status == agentlib.AgentStatusDone`, `result.NextPhase == "human_review"`, `review.Approved` true, and `review.Notes` contains neither `"tag leaked"` nor `"expected OPEN"` — the merged, clean-and-shipped PR is approved with no `status=failed` re-file. (The outer `BeforeEach` already provides the compliant CHANGELOG via `CloneAtRefStub` and green gates.)

5. **Design doc.** Update `docs/design.md` to record both corrected behaviors (do NOT touch any other section — byte-stable elsewhere):
   - **§ 4.3 ai_review "Side effects" row:** in the cell that currently reads `gh pr view --json state,isDraft; fresh worktree @ branch; ... ; git ls-remote --tags shows no tag at branch commits`, replace the two stale fragments — `gh pr view --json state,isDraft` gains `(a MERGED PR is the shipped state — accepted, no "expected OPEN" rejection)`, and `git ls-remote --tags shows no tag at branch commits` becomes `git ls-remote --tags shows no tag at branch-introduced commits (a tag on a base-reachable release-history commit is not a leak)`. Keep the rest of the cell (fresh worktree, gate re-run, CHANGELOG bullet) unchanged.
   - **§ 4.4 State passing + invariants:** in the invariants paragraph, after the existing `Review.checks.gate_green derived from re-execution, not from Result.gate_exit` invariant, add: `Review.checks.no_new_tag compares remote tags against git rev-list origin/master..HEAD — only branch-introduced commits count; a tag on a base-reachable release-history commit is not a leak. A MERGED PR is the shipped success state and is accepted (routes human_review so the operator closes the task); only non-shipped states (e.g. CLOSED) are rejected with "expected OPEN".`
   - **§ 8.1 ai_review acceptance row:** extend the cell that currently reads `## Review all-true on the happy path; seeded broken check (e.g. deleted PR) → approved: false + park` with `; an already-MERGED PR is accepted as shipped (routes human_review, no re-file)`.

6. **Changelog.** `CHANGELOG.md` already has an `## Unreleased` section (spec 004 entries + the sibling prompt's bullet). Append one new bullet under it (do not touch the existing bullets or released headers):
   `- fix: the ai_review step accepts an already-MERGED update PR as the shipped success state instead of rejecting it with "pr state is MERGED, expected OPEN" — a merged task routes to human_review for the operator to close rather than publishing status=failed and re-filing the task forever`
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- No config fields, no opt-out flags, no new tunables (spec Constraints). There is NO new verdict or phase — a MERGED PR routes through the existing approved path (approved → human_review) exactly like an OPEN one.
- The `GhCli` interface (`ViewPR`) and the `GitOps` interface are UNCHANGED — no seam signature edits; do NOT hand-edit `mocks/` (regenerated by `make generate`, part of `make precommit`).
- The serialized `pr_open` and `pr_draft` keys keep their names and meanings — `pr_open` is the raw "is the PR open" truth (false for MERGED); the acceptance decision lives in the review verdict, not the key.
- The check contract stays fail-closed for genuine anomalies: a CLOSED or otherwise surprising non-shipped PR state still surfaces "pr state is X, expected OPEN" and rejects (AC4).
- This prompt touches ONLY `checkPR`, the `checkPR` call-site line in `Run` (the aggregation), the tests/CHANGELOG, and the three `docs/design.md` passages named above. Do NOT modify `checkNoNewTag`, `checkGates`, `checkChangelog`, `finish`, or `writeYourMoveSection`.
- Never `fmt.Errorf`; use `github.com/bborbe/errors` where new errors are introduced (the package idiom). Never `context.Background()` in non-test `pkg/` code.
- No new module dependencies; do NOT run `go mod vendor` and do NOT use `-mod=vendor` (the repo does not commit `vendor/`; use `-mod=mod`).
- Keep functions inside the `funlen` limit (80 lines / 50 statements) and lines under 100 chars (`golines`).
- Existing tests must still pass; new code needs ≥80% statement coverage.
</constraints>

<verification>
Run `make precommit` — must exit 0 (regenerates mocks — unchanged — and runs tests + linters).

Run `make test` — all tests pass, including the new MERGED and repro rows in `pkg/steps_review_test.go`.

```bash
grep -n 'MERGED' pkg/steps_review.go
# expect: the MERGED acceptance guard present in checkPR

grep -n 'expected OPEN' pkg/steps_review.go
# expect: exactly 2 occurrences — the code's non-shipped-state rejection note
# plus the checkPR doc-comment sentence (req 3) that quotes it

grep -n 'pr state is ' pkg/steps_review_test.go
# expect: the CLOSED row asserting the "pr state is CLOSED, expected OPEN" note

grep -n 'MERGED\|branch-introduced\|origin/master' docs/design.md
# expect: the § 4.3 side-effects, § 4.4 invariant, and § 8.1 updates present
```

```bash
go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'steps_review|total'
# expect: the changed functions at or near 100%; the total not lower than before
```
</verification>
