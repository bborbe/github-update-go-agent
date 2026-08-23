---
status: completed
spec: [005-bug-ai-review-leaked-tag-and-merged-false-positive]
summary: Changed the ai_review no_new_tag check to compare remote tags against git rev-list origin/master..HEAD (the update branch's own commits) instead of the stale pinned filing ref, dropped the ref param and frontmatter-ref guard, added AC1/AC2/failure-mode unit tests, and documented the fix in the CHANGELOG
execution_id: github-update-go-agent-exec-010-spec-005-check-no-new-tag-base-reachable
dark-factory-version: dev
created: "2026-08-24T09:30:00Z"
queued: "2026-08-23T23:30:12Z"
started: "2026-08-23T23:30:13Z"
completed: "2026-08-23T23:34:36Z"
branch: dark-factory/bug-ai-review-leaked-tag-and-merged-false-positive
---

<!-- NOTE FOR THE AUDITOR (not part of the executable task):
Decomposition decision: spec 005's Suggested Decomposition lists a third
"release + deploy" prompt (v0.12.5, kubectl deploys, Rung-2 replay). Those
steps are operator-executable only (no Docker/kubectl/gh release in a YOLO
container) and the spec's own Verification ladder already covers them
("Operator-executable" rung: release v0.12.5, deploy dev+prod, repro replay).
Per dark-factory rules, operator-only verification belongs on the spec, not in
a prompt — so no third container prompt is emitted here. The container-side of
AC5 (make test green + changed review logic producing an approved verdict on
the repro shape) is covered by unit tests across this prompt and its sibling
(spec-005-check-pr-merged-shipped.md), and the CHANGELOG Unreleased bullets
are added by each implementation prompt (the v0.12.5 version-header bump and
git tag are the releaser's job, per the repo's release-commit history).

Root-cause reconciliation: the spec's "Why this is a bug" text says the check
"compares tags against the whole reachable-from-base commit set". The actual
mechanism, verified against pkg/git/os_exec_git_ops.go RevList (git rev-list
<base>..HEAD) and v0.12.4's run-start resolution (ResolveDefaultBranchHead),
is: the update branch is created from the RESOLVED current master HEAD (which
may be newer than the pinned filing ref), so RevList(pinned-ref) = git rev-list
<stale ref>..HEAD includes the master commits added after filing — including
legitimate release tags on master. The fix (base the range on origin/master,
the PR's actual base) makes the documented intent hold in all cases. The
Acceptance Criteria and Desired Behaviors are implemented exactly as written.
-->

# Exclude base-reachable tags from the ai_review no_new_tag check

<summary>
- The ai_review no_new_tag check now compares remote tags against only the commits the update branch itself introduced, not against the whole history reachable from a possibly-stale pinned filing SHA.
- A remote tag whose commit is already reachable from the PR base (legitimate release history on master) is no longer misreported as "a tag leaked from the update pipeline".
- A remote tag on a commit the update branch actually created still fails the check exactly as before — the leak gate is not weakened for real anomalies.
- The check derives the branch's own commits from origin/master (the PR's actual base in the fresh review worktree) instead of the pinned frontmatter ref, so release tags added to master after the task was filed are correctly excluded.
- A missing frontmatter ref no longer blocks the tag check — the ref is no longer needed for it, so the old "cannot compute branch commits" note is gone.
- Existing failure handling is unchanged and stays fail-closed: a failed git rev-list or git ls-remote --tags still surfaces its note and leaves the check unsatisfied.
- The GitOps interface is untouched — no seam signature changes, no mock hand-editing.
- New unit tests lock the base-reachable-excluded and branch-introduced-still-flagged rows, and the CHANGELOG documents the fix.
</summary>

<objective>
Make the ai_review no_new_tag check flag a remote tag only when it points at a commit the update branch itself introduced — computing that set from origin/master (the branch's actual base) rather than the stale pinned filing ref — so legitimate release tags already on master history are never misreported as leaks, while a genuine branch-introduced tag still rejects the review.
</objective>

<context>
Read the injected container `CLAUDE.md` (`/home/node/.claude/CLAUDE.md`) for project conventions — there is no repo-root CLAUDE.md.

Read fully before changing anything:
- `pkg/steps_review.go` — the whole file: `reviewStep.Run` (the ai_review pipeline; note `ref, _ := md.Frontmatter.String("ref")` at the top and the `s.checkNoNewTag(ctx, workdir, authedURL, ref, &checks, &notes)` call inside the clone-success branch), `checkNoNewTag` (the current `RevList(ctx, workdir, ref)` base and its `frontmatter ref missing` guard), and `checkChangelog` — the in-file precedent for using the literal `"origin/master"` ref against the worktree.
- `pkg/git/git.go` — the `GitOps` interface, `RevList(ctx, workdir, base string) ([]string, error)` and its doc comment ("the commit SHAs on HEAD that are not reachable from base (git rev-list <base>..HEAD) — the branch's own commits"). Do NOT change this seam.
- `pkg/git/os_exec_git_ops.go` — the `osExecGitOps.RevList` implementation (`git -C <workdir> rev-list <base>..HEAD`) and `LsRemoteTags`, so you know exactly what base argument the review worktree accepts (a ref name like `origin/master` works — the clone is full, `origin/master` is present).
- `pkg/review_output.go` — the `ReviewChecks` struct and the serialized `no_new_tag` key (must keep its meaning: "no tag at a branch-introduced commit").
- `pkg/steps_review_test.go` — the Ginkgo `Describe("ReviewStep", ...)` suite: the outer `BeforeEach` fakes (`ops.RevListReturns([]string{"deadbeef1", "deadbeef2"}, nil)`, `ops.LsRemoteTagsReturns([]string{"1111111", "2222222"}, nil)`), the existing `Describe("tag leaked onto a branch commit", ...)` (the AC2 row — keep it), and the counterfeiter accessors used below (`ops.RevListArgsForCall(i)` returns `(context.Context, string, string)` — workdir, base; `ops.LsRemoteTagsArgsForCall(i)` returns `(context.Context, string)`).
- The spec `specs/in-progress/005-bug-ai-review-leaked-tag-and-merged-false-positive.md` — the Goal, Desired Behaviors 1–3, Failure Modes rows 1–3 (rev-list fail, ls-remote fail, force-pushed base), and Acceptance Criteria AC1–AC2.
- `docs/design.md` § 4.3 ai_review "Side effects" row — read for orientation only; the design-doc wording is updated by the sibling prompt.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` usage; never `fmt.Errorf`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage ≥80%.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits: `funlen` (80 lines / 50 statements), `golines` 100.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `## Unreleased` placement and entry style.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.

Run this first to confirm the exact call sites and mock accessors:

```bash
grep -n 'checkNoNewTag\|RevList\|origin/master' pkg/steps_review.go
grep -n 'RevListArgsForCall\|RevListReturns\|LsRemoteTagsReturns' mocks/git_ops.go
grep -n 'RevListArgsForCall\|LsRemoteTagsReturns\|LsRemoteTagsArgsForCall' pkg/steps_review_test.go
```
</context>

<requirements>
1. **Change the base the tag check compares against.** In `pkg/steps_review.go`, `checkNoNewTag` currently builds the branch-commit set with `s.ops.RevList(ctx, workdir, ref)` where `ref` is the pinned filing SHA. The branch's actual base is the PR base — `origin/master` (the execution step resolves and clones the current master HEAD at run start, so the branch is a linear child of current master; `checkChangelog` already uses the literal `"origin/master"` against the same worktree). Change the call to:
   ```go
   branchCommits, err := s.ops.RevList(ctx, workdir, "origin/master")
   ```
   The `RevList` seam is unchanged (still `git rev-list <base>..HEAD`); only the base argument changes. This makes the check compare tags against the branch's OWN commits — a release tag on a master-history commit (reachable from origin/master) is no longer in the set, while a tag on a branch-introduced commit still is.

2. **Drop the now-unused `ref` parameter and guard.** `checkNoNewTag` no longer needs the pinned ref. Remove:
   - the `ref string` parameter from the `checkNoNewTag` signature;
   - the `if ref == "" { *notes = append(*notes, "frontmatter ref missing — cannot compute branch commits"); return }` guard (the branch-commit set now derives from `origin/master`, so a missing ref is irrelevant);
   - the `ref, _ := md.Frontmatter.String("ref")` line in `reviewStep.Run` (it becomes an unused local once its only consumer is gone — Go rejects unused locals) and pass only `(ctx, workdir, authedURL, &checks, &notes)` at the call site.
   The resulting signature is:
   ```go
   func (s *reviewStep) checkNoNewTag(
   	ctx context.Context,
   	workdir, authedURL string,
   	checks *ReviewChecks,
   	notes *[]string,
   )
   ```

3. **Update the `checkNoNewTag` doc comment** so it states the base-reachable exclusion contract explicitly — e.g. "checkNoNewTag verifies the remote holds no tag pointing at any commit the update branch itself introduced (git ls-remote --tags vs `git rev-list origin/master..HEAD`). A tag on a commit already reachable from the PR base — legitimate release history on master — is not a leak; only a tag at a branch-introduced commit fails the check (fail-closed)." Keep it inside the `funlen`/`golines` limits.

4. **Keep the existing failure handling exactly as is** (spec Failure Modes rows 1 and 3, and the fail-closed contract):
   - `RevList` error → the existing `"rev-list failed: " + err.Error()` note and `NoNewTag` stays false (no return value set);
   - `LsRemoteTags` error → the existing `"ls-remote --tags failed: " + git.RedactToken(err.Error())` note and fail-closed;
   - `ref == ""` is no longer a failure path (guard removed per step 2) — do NOT reintroduce any ref-based early return.
   Do not touch `checkPR`, `checkGates`, `checkChangelog`, or the approval aggregation in `Run`.

5. **Unit tests** in `pkg/steps_review_test.go` (Ginkgo, inside the existing `Describe("ReviewStep", ...)` suite, external `pkg_test` package). Add a new `Describe("tag reachable from the base branch (release history)", ...)` with a nested `BeforeEach`, and a new `It` in the existing happy-path `Describe`. The distinguishing assertion is the base argument passed to `RevList` — this is what separates the fixed behavior from the old stale-ref behavior:
   - **AC1 — base-reachable release tag is not flagged:** in the new `Describe`, set
     ```go
     ops.RevListStub = func(_ context.Context, _, base string) ([]string, error) {
     	// The stale pinned ref would include master commits added after filing
     	// (e.g. a release commit); origin/master (the branch's actual base)
     	// yields only the branch's own commits.
     	if base == "origin/master" {
     		return []string{"f8b922c2"}, nil
     	}
     	return []string{"f8b922c2", "6e16a948"}, nil
     }
     ops.LsRemoteTagsReturns([]string{"6e16a948"}, nil)
     ```
     and an `It` asserting: the review is approved with `review.Checks.NoNewTag == true`, `review.Notes` does not contain `"tag leaked"`, and — the load-bearing assertion — `_, _, base := ops.RevListArgsForCall(0)` satisfies `base == "origin/master"` (proves the check no longer feeds the pinned ref to `RevList`). Under the old code this row fails (the stub returns `6e16a948` for any non-origin/master base, the note fires, `NoNewTag` is false).
   - **AC2 — branch-introduced tag still flagged:** keep the existing `Describe("tag leaked onto a branch commit", ...)` test (its `LsRemoteTagsReturns([]string{"deadbeef2"}, nil)` overlaps the outer `RevListReturns` set `["deadbeef1","deadbeef2"]` and asserts `review.Checks.NoNewTag == false`). Add to the same `It` an assertion that the rejection note contains `"tag leaked"` — the genuine-leak note must still fire unchanged.
   - **Regression guard on the happy path:** in the outer happy-path `Describe`, add one `It` asserting the review approves and that `RevList` was invoked with `"origin/master"`:
     ```go
     It("bases the tag check on origin/master, the PR base", func() {
     	_, err := step.Run(ctx, md)
     	Expect(err).To(BeNil())
     	_, _, base := ops.RevListArgsForCall(0)
     	Expect(base).To(Equal("origin/master"))
     })
     ```
   - **Failure-mode rows (spec rows 1 and 3):** add two `It`s under a nested `Describe("rev-list / ls-remote failure", ...)`:
     ```go
     It("keeps NoNewTag false when git rev-list fails", func() {
     	ops.RevListReturns(nil, stderrors.New("rev-list boom"))
     	result, err := step.Run(ctx, md)
     	Expect(err).To(BeNil())
     	Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
     	review, _ := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
     	Expect(review.Checks.NoNewTag).To(BeFalse())
     	Expect(review.Notes).To(ContainSubstring("rev-list failed"))
     })
     It("keeps NoNewTag false when git ls-remote --tags fails", func() {
     	ops.LsRemoteTagsReturns(nil, stderrors.New("ls-remote boom"))
     	result, err := step.Run(ctx, md)
     	Expect(err).To(BeNil())
     	Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
     	review, _ := agentlib.ExtractSection[pkg.ReviewOutput](ctx, md, "## Review")
     	Expect(review.Checks.NoNewTag).To(BeFalse())
     	Expect(review.Notes).To(ContainSubstring("ls-remote --tags failed"))
     })
     ```
     Note: `stderrors` is already imported in `pkg/steps_review_test.go` (the existing `ViewPR error` row uses it). `agentlib` and `pkg` are already imported.

6. **Changelog.** `CHANGELOG.md` already has an `## Unreleased` section (spec 004 entries). Append one new bullet under it (per the changelog guide — one bullet per logical change; do not touch the existing bullets or the released headers):
   `- fix: the ai_review step's no_new_tag check compares remote tags against only the update branch's own commits (git rev-list origin/master..HEAD) instead of the whole history reachable from the pinned filing ref — a legitimate release tag already on master is no longer misreported as a tag leaked from the update pipeline, while a tag on a branch-introduced commit still rejects the review`
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- No config fields, no opt-out flags, no new tunables (spec Constraints).
- The `GitOps` interface (`RevList`, `LsRemoteTags`, `CloneAtRef`, …) is UNCHANGED — no seam signature edits; do NOT hand-edit `mocks/` (regenerated by `make generate`, part of `make precommit`).
- The check contract stays fail-closed for genuine anomalies: a real branch-introduced leak still rejects (AC2); a failed `rev-list` or `ls-remote --tags` still surfaces its note and leaves `NoNewTag` false.
- The serialized `no_new_tag` key keeps its name and meaning — "no tag at a branch-introduced commit".
- This prompt touches ONLY `checkNoNewTag`, its caller line in `Run`, and the tests/CHANGELOG named above. Do NOT touch `checkPR` (the MERGED-state fix is the sibling prompt) or the approval aggregation in `Run`.
- Never `fmt.Errorf`; use `github.com/bborbe/errors` where new errors are introduced (the package idiom). Never `context.Background()` in non-test `pkg/` code.
- No new module dependencies; do NOT run `go mod vendor` and do NOT use `-mod=vendor` (the repo does not commit `vendor/`; use `-mod=mod`).
- Keep functions inside the `funlen` limit (80 lines / 50 statements) and lines under 100 chars (`golines`).
- Existing tests must still pass; new code needs ≥80% statement coverage.
</constraints>

<verification>
Run `make precommit` — must exit 0 (regenerates mocks — unchanged — and runs tests + linters).

Run `make test` — all tests pass, including the new base-reachable and rev-list/ls-remote failure rows in `pkg/steps_review_test.go`.

```bash
grep -n 'RevList(ctx, workdir' pkg/steps_review.go
# expect: exactly 1 line — RevList(ctx, workdir, "origin/master")

grep -n 'RevList(ctx, workdir, ref)' pkg/steps_review.go
# expect: 0 lines — the stale pinned-ref base is gone

grep -n 'frontmatter ref missing' pkg/steps_review.go
# expect: 0 lines — the obsolete ref guard is removed

grep -n 'origin/master' pkg/steps_review.go
# expect: checkChangelog's ShowFile ref + checkNoNewTag's RevList base both present
```

```bash
go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'steps_review|total'
# expect: the changed functions at or near 100%; the total not lower than before
```
</verification>
