---
status: approved
spec: [004-clone-current-master-for-bump]
created: "2026-08-23T19:40:00Z"
queued: "2026-08-23T19:39:25Z"
---

# Update docs/design.md clone-at-ref semantics to run-start resolution

<summary>
- The design doc no longer claims the agent clones the task's pinned ref — the three stale "worktree @ ref" / "clone at ref" passages are rewritten for the run-start-resolution behavior.
- The task-format section now explains that the pinned `ref` is the filing-time SHA, recorded for provenance and dedupe, while the clone base is the repo's current default-branch HEAD resolved at run start.
- The planning per-phase table records the resolve-at-run-start side effect and the resolution-failure escalation (`failed` naming the resolution step).
- The execution per-phase table records that the worktree is at the current default-branch HEAD resolved at its own run start, while the deterministic branch name still derives from the pinned filing SHA.
- The documented `Result.branch` invariant stays accurate — the branch name keeps deriving from the pinned `ref`, not the resolved HEAD.
- The review step's `origin/master` comparison is explicitly left as-is; no behavior, code, or watcher-filing semantics change.
</summary>

<objective>
Bring `docs/design.md` in line with spec 004's run-start-resolution behavior — the agent resolves and clones the repo's current default-branch HEAD at each run's start, the pinned filing SHA is provenance-only (still driving the deterministic branch name), and a resolution failure stops the run loudly — so the design doc no longer describes stale clone-at-ref semantics.
</objective>

<context>
Read the injected container `CLAUDE.md` (`/home/node/.claude/CLAUDE.md`) for project conventions — there is no repo-root CLAUDE.md.

Read fully before changing anything:
- `docs/design.md` — specifically § 3.3 (Task format, the frontmatter block whose `clone_url`/`ref` lines carry the "agent rewrites to https for App-token auth" / `<full HEAD SHA>` comments), § 4.3 (the `planning` table's "Input", "Side effects" and "Failure" rows, and the `execution` table's "Side effects" row), and § 4.4 (State passing + invariants, the `Result.branch == "fix/update-go-" + ref[:7]` invariant).
- The spec `specs/in-progress/004-clone-current-master-for-bump.md` — the Goal (planning + bump + precommit gate run against the repo's current default-branch HEAD at run start), Non-goals (the watcher's task filing and `sha_unchanged` dedupe unchanged; the review step's `origin/master` comparison unchanged; the pinned SHA stays recorded for provenance), and the Constraints row naming `docs/design.md`.

No code files are touched by this prompt.
</context>

<requirements>
1. **§ 3.3 Task format — annotate `ref` as provenance-only.** In `docs/design.md`, in the frontmatter code block of the "Task format" subsection, update the line carrying `ref: <full HEAD SHA>` (currently followed by the `current_go`/`latest_go` watcher-signal lines) so the inline comment states that `ref` is the filing-time SHA recorded for provenance and dedupe only, and that the agent clones the repo's current default-branch HEAD resolved at run start. Keep the field names, their order, and the surrounding text otherwise unchanged — the watcher's task filing itself is not changing (spec Non-goal). Suggested wording for the line: `ref: <full HEAD SHA>   # filing SHA — provenance + dedupe only; the agent clones the current default-branch HEAD resolved at run start`.

2. **§ 4.3 planning — "Input" row.** Update the planning table's `Input` row (`frontmatter repo, clone_url, ref, update_scope (optional; default both)`) so it notes that `ref` is read for provenance and branch determinism, not as the clone base — e.g. append `— ref is provenance/branch-name only; clone base is the resolved default-branch HEAD`.

3. **§ 4.3 planning — "Side effects" row.** Replace the `bare-clone + worktree @ ref (read-only wrt origin)` fragment with resolve-at-run-start wording, e.g. `resolve the repo's current default-branch HEAD at run start; bare-clone + worktree @ resolved HEAD (read-only wrt origin)`. Keep the rest of the row (gate-target detection, scanner targets, outdated-deps enumeration) unchanged.

4. **§ 4.3 planning — "Failure" row.** Add the resolution-failure escalation to the planning `Failure` row, alongside the existing clone/auth entry — e.g. append `current-HEAD resolution fail → failed naming the resolution step` (the run never falls back to the stale pinned ref).

5. **§ 4.3 execution — "Side effects" row.** Replace the `worktree @ ref; git switch -c fix/update-go-<sha:7>` opening with run-start-resolution wording that keeps the deterministic branch name pinned to the filing SHA, e.g. `worktree @ current default-branch HEAD (resolved at run start); git switch -c fix/update-go-<ref:7> (ref = pinned filing SHA — deterministic replay guard)`. Leave the rest of the row (D2 update sequence, repair, CHANGELOG bullet, gate run, commit, push, PR creation) unchanged.

6. **§ 4.4 State passing + invariants.** The invariant `Result.branch == "fix/update-go-" + ref[:7]` stays true — extend it with a parenthetical clarifying that `ref` is the pinned frontmatter filing SHA, not the resolved HEAD, e.g. `Result.branch == "fix/update-go-" + ref[:7] (ref = the pinned frontmatter filing SHA, not the resolved HEAD)`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Documentation only: do NOT change any Go code, the watcher's task filing, the `sha_unchanged` dedupe, the gate contract, or the review step's `origin/master` comparison.
- The task-format field list and order in § 3.3 stay unchanged — only the `ref` inline comment and any directly adjacent explanatory text are updated.
- No new config fields, no opt-out flags, no tunable thresholds.
- Keep the design doc's other sections (identity, integration, safety, acceptance) byte-stable — only the clone-at-ref passages named in the requirements above are edited.
</constraints>

<verification>
```bash
grep -n 'worktree @ ref\|bare-clone + worktree @ ref\|clone at ref' docs/design.md
# expect: 0 lines — no stale clone-at-ref phrasing remains

grep -n 'resolved HEAD\|current default-branch HEAD\|filing SHA' docs/design.md
# expect: the new run-start-resolution wording present in § 3.3, § 4.3 planning + execution, and § 4.4
```
</verification>
