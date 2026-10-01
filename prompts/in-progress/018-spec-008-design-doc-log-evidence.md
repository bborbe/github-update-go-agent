---
status: approved
spec: [008-bug-build-fix-log-truncated-to-head]
created: "2026-10-01T06:37:44Z"
queued: "2026-10-01T06:53:31Z"
branch: dark-factory/bug-build-fix-log-truncated-to-head
---

# Record the build-fix log-evidence contract in docs/design.md

<!--
REVIEWER NOTE — not an instruction to the executing agent.
Heading-level open question: spec 008 AC6's evidence greps `^### .*[Ll]og evidence`, i.e. a
THREE-hash heading. `docs/design.md` today has NO `###` headings — it uses `# N.` for top-level
sections and `## N.M` for subsections. The spec's machine-checked grep wins, so this prompt pins
the contract heading at `### ` and tells the executor not to "fix" it to `##`. If the reviewer
would rather match the file's existing `##` subsection level, the spec's AC6 grep must change too.
-->

<summary>
- The design document gains the build-fix lane's first contract section — until now it described the update lane only.
- The section records three invariants: the retained end is the log's tail, the marker names the dropped-line count, and the bound stays at 200 lines.
- A reader can see why a head-truncated log was the wrong slice and what the diagnosis evidence now contains.
- Documentation only — no Go code changes; the behavior shipped in the preceding prompt (017).
- The section is placed under a new build-fix-lane heading, before the document's Related section.
</summary>

<objective>
Add the build-fix lane's log-evidence contract to `docs/design.md` — the retained end is the log's tail, the marker names the dropped count, and the bound stays at 200 lines — so the lane that produces build-fix escalations is documented where it was previously absent.
</objective>

<context>
Read `/home/node/.claude/CLAUDE.md` for project conventions — there is no repo-root `CLAUDE.md`.

Read fully before changing anything:

- `docs/design.md` — the target. Note its heading style: `# N. Title` for top-level sections (`# 1. Motivation` … `# 8. Acceptance`) and `## N.M Title` for subsections; the file ends with an unnumbered `# Related` section (currently line 261). There are currently zero `### ` headings in the file. The build-fix lane (`pkg/steps_fix_*.go`) is not mentioned anywhere in the file today.
- `pkg/gh_cli.go` — read `truncateToLines` so the documented contract matches the shipped code exactly (the retained slice, the marker format, and the within-bound branch). Do NOT change it.
- Spec: `specs/in-progress/008-bug-build-fix-log-truncated-to-head.md` — AC6 is this prompt's scope; Desired Behavior items 2, 3, and 5 are the three invariants you record.

- Coding guides (in-container paths):
  - `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md` — doc style.
  - `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — for reference only; do NOT edit the changelog in this prompt.

Three hard shape constraints, because the spec's acceptance evidence greps for them:
- The contract heading must begin with `### ` (three hashes) — the spec greps `^### .*[Ll]og evidence`.
- The literal phrase `diagnosis-sized log tail` must appear (the spec's verification greps `diagnosis-sized log tail`).
- Within the 12 lines following the heading there must be at least three lines containing one of `tail`, `dropped`, `200`.

Run this first to see the current state:
```bash
grep -n '^### ' docs/design.md            # expect: no output
grep -n '^# Related' docs/design.md        # the insertion point
grep -n 'truncateToLines' pkg/gh_cli.go
```
</context>

<requirements>
1. **Insert a new top-level section immediately before `# Related`** in `docs/design.md`. Insert exactly:

```
# 9. Build-fix lane

The build-fix lane (`pkg/steps_fix_planning.go`, `pkg/steps_fix_execution.go`, `pkg/steps_fix_review.go`) classifies and repairs a red build without an operator.
```

   Keep one blank line before `# 9.` and one blank line between the intro sentence and the next heading.

2. **Add the log-evidence contract subsection** directly under the section you just inserted, using exactly this content (the `### ` heading level is required — see the shape constraints above; do not "correct" it to `##`):

```
### Log evidence

`truncateToLines` bounds the log handed to the diagnosis model to a **diagnosis-sized log tail**: at most 200 lines.

- The retained end is the **tail** — a CI job log's failing step is at the end (`gh run view --log-failed` returns the failed job in execution order), so runner provisioning and checkout lead the log and the failure trails it.
- When earlier lines are dropped, the evidence is prefixed by a marker naming the **dropped** count (e.g. `... (449 lines dropped)`), so a truncated head is distinguishable from a truncated tail.
- The bound stays at **200** lines; an input already within the bound is returned verbatim, with no marker.
```

   Resulting order in the file: `# 8. Acceptance` … `# 9. Build-fix lane` → intro sentence → `### Log evidence` → the four content lines → `# Related`.

3. **Leave everything else byte-stable.** Do not reword, renumber, or reformat any existing section. Do not modify `CHANGELOG.md` (the preceding prompt already owns the single `## Unreleased` entry).

4. **Before you finish**, confirm the documented behavior matches the shipped code: `grep -n 'lines\[len(lines)-n:\]' pkg/gh_cli.go` must return one match and the marker must be the `... (%d lines dropped)` form. If `truncateToLines` still keeps the head (`lines[:n]`), STOP — write nothing, do not implement or stub the code, and report the run as failed with the explicit message `log-tail bound not yet deployed (prompt 017)`. Then re-run every `<verification>` command and confirm each expected result.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Documentation only: do NOT modify any `.go` file, and do NOT touch `pkg/`, `main.go`, `cmd/`, or `pkg/prompts/`.
- Do NOT modify `CHANGELOG.md`.
- The contract heading must be `### Log evidence` (three hashes) — the spec's acceptance evidence greps `^### .*[Ll]og evidence`. Do not change it to `##` to match the file's existing subsection level.
- The literal phrase `diagnosis-sized log tail` must appear in the section.
- Do NOT add a `|` character anywhere in the new prose (no table is being added).
- Do NOT invent extra invariants, config fields, opt-out flags, or tunable thresholds — record only the three named invariants (tail retained, dropped count named, bound stays 200).
- The repo's gate (`make precommit`) stays green.
- Container-autonomous: file edits + `make`/`go` only. No `kubectl`, no `docker`, no `gh`, no PR/deploy steps. Do NOT run `git` commands — this container's `.git` is masked.
</constraints>

<verification>
Run in `/workspace`:

```bash
grep -c '^### .*[Ll]og evidence' docs/design.md
# expect: >= 1

grep -A 12 '^### .*[Ll]og evidence' docs/design.md | grep -cE 'tail|dropped|200'
# expect: >= 3

grep -c 'diagnosis-sized log tail' docs/design.md
# expect: >= 1

grep -n '^# 9. Build-fix lane' docs/design.md
# expect: exactly 1 match, positioned before '# Related'
```

Then:
```bash
make precommit
# expect: exit 0
```
</verification>
