---
status: completed
spec: [007-external-advisory-ingestion]
summary: 'Documented the external-advisory contract in docs/design.md (planning Input row, ai_review Side effects row, §3.3 sample plus contract paragraph, §5.1 Inputs) and created the CHANGELOG ## Unreleased entry; make precommit exits 0.'
execution_id: github-update-go-agent-external-advisory-ingestion-exec-016-spec-007-docs-advisory-contract
dark-factory-version: dev
created: "2026-09-16T19:50:00Z"
queued: "2026-09-16T17:44:39Z"
started: "2026-09-16T17:59:42Z"
completed: "2026-09-16T18:04:39Z"
branch: dark-factory/external-advisory-ingestion
---

# Document the external advisory contract and record it in the changelog

<summary>
- The design document now records that planning reads an advisory block from the task frontmatter, alongside the existing inputs.
- The design document now records that the review's vulnerability verdict for an external advisory comes from an independent installed-version check on the branch, not from the repo's scanners coming back green.
- The task-format section carries the whole producer-facing contract in one place: the four keys, the accepted ID shapes, and what happens when a block is malformed.
- The inputs section names the block, so a producer looking for the interface finds it without reading the Go.
- The changelog carries one entry describing the ingestion and its targeted verification, under a new Unreleased heading.
- No code changes: this prompt documents behavior that already shipped in the two preceding prompts.
</summary>

<objective>
Record the external advisory contract where its producer and its operators will look: the design document's planning-inputs row, the review's side-effects row, the task-format section, and the inputs section — plus one changelog entry under a newly created `## Unreleased` heading.
</objective>

<context>
Read `README.md` (repo root) and `docs/design.md` for project conventions (`CLAUDE.md` is not present in this repo).

Read before changing anything:

- `specs/in-progress/007-external-advisory-ingestion.md` — the approved spec. Acceptance Criteria AC9 and AC10 are this prompt's scope; the "Assumptions" and "Constraints" sections describe the contract you are documenting.
- `docs/design.md` — the target. Relevant anchors, all verified as of this writing:
  - line 80 `## 3.3 Task format`, line 100 `## 3.4 Upstream dependencies` — the range AC9 checks for the block contract.
  - line 130 — the planning `Input` row, which begins `| Input | frontmatter \`repo\`, ...`.
  - line 162 — the ai_review `Side effects` row, which begins ``| Side effects | `gh pr view ...``.
  - line 177 `## 5.1 Inputs`, line 180 `## 5.2 Outputs`.
- `CHANGELOG.md` — line 8 is `## v0.17.13`; the file starts directly at a released section, so `## Unreleased` does not exist yet and must be created above `## v0.17.13`.
- `pkg/advisory.go` and `pkg/steps_review.go` — read them so the documented behavior matches the shipped behavior exactly (the four keys, the accepted ID shapes, the rejection classes, the fail-closed review path). Do NOT change either file.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — entry format: `- <prefix>: <what> [context]`, one bullet per logical change, specific over generic.
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md` — doc style.

Two hard shape constraints on the edits below, because the spec's acceptance evidence greps for them:

- The planning `Input` row and the ai_review `Side effects` row must each stay a SINGLE markdown table line that still begins with its current prefix. Extend the existing line in place; do not wrap it, do not split it into two rows, and do not introduce a new row that repeats the prefix.
- Do not add a `|` character inside either table cell — it would terminate the cell and break the table.

Run this first to see the current state of every anchor:

```bash
grep -n '^| Input |' docs/design.md
grep -n '^| Side effects |' docs/design.md
sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md
sed -n '/^## 5.1 Inputs/,/^## 5.2/p' docs/design.md
sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md
```
</context>

<requirements>
1. **Extend the planning `Input` row** in `docs/design.md` (the line beginning ``| Input | frontmatter `repo` ``) so it also names the optional `advisory` frontmatter block. The row must remain one line, keep its current prefix, and end up containing the word `advisory`. Resulting row:

   ```
   | Input | frontmatter `repo`, `clone_url`, `ref`, `update_scope` (optional; default `both`), `advisory` (optional; one externally-supplied advisory block — keys `id`, `package`, `fixed_version`, `source`) — `ref` is provenance/branch-name only; clone base is the resolved default-branch HEAD |
   ```

2. **Extend the ai_review `Side effects` row** in `docs/design.md` (the line beginning ``| Side effects | `gh pr view ``) so it states that `vulns_clear` for an external advisory comes from an independent installed-version check on the branch. The row must remain one line, keep its current prefix, and end up containing the exact substring `installed version`. Resulting row:

   ```
   | Side effects | `gh pr view --json state,isDraft` (a MERGED PR is the shipped state — accepted, no "expected OPEN" rejection); fresh worktree @ branch; re-run gate targets; for a task whose frontmatter carries an `advisory` block, resolve the advisory's package in the branch's module graph and require its installed version to be at or above the advisory's `fixed_version` — an independent Go check that never reads `## Plan` or `## Result` and fails closed when the version is undeterminable; verify CHANGELOG bullet under `## Unreleased` and no new `## vX.Y.Z` header; `git ls-remote --tags` shows no tag at branch-introduced commits (a tag on a base-reachable release-history commit is not a leak) |
   ```

3. **Record the block contract in §3.3 Task format** (`docs/design.md`, between `## 3.3 Task format` and `## 3.4 Upstream dependencies`), where a producer finds it. Two additions:

   a. Add the block to the existing fenced `yaml` sample, after the `latest_go:` line and inside the same fence:

   ```yaml
   advisory:              # OPTIONAL — at most one externally-supplied advisory per task
     id: CVE-2026-12345   # GO-<year>-<n> | CVE-<year>-<n> | GHSA-<grp>-<grp>-<grp>
     package: golang.org/x/text
     fixed_version: v0.39.0
     source: osv-feed     # the row's scanner label becomes external:<source>
   ```

   b. Add a paragraph after the fence (after the existing `Body = operator-readable header only; never a data source.` line) stating the contract in full:

   ```
   `advisory` is the frozen external-advisory contract: a single mapping whose four keys `id`, `package`, `fixed_version` and `source` are all required and non-empty; unrecognized extra keys are ignored. The accepted ID shapes are `GO-<year>-<n>`, `CVE-<year>-<n>` and `GHSA-xxxx-xxxx-xxxx`. A block that fails validation — an ID outside those shapes, an empty `package`, an unparseable `fixed_version`, a missing key, or a list instead of a mapping — ends planning with `needs_input` naming the offending field and its raw value, before any gate target runs; the same block failing to validate at review time fails the review closed instead. A second advisory written under a different key name is not the block and has no effect — file one task per advisory.
   ```

   The §3.3 range must end up containing each of: `advisory`, `fixed_version`, `CVE-`, `GHSA-`, `needs_input`.

4. **Name the block in §5.1 Inputs** (`docs/design.md`, between `## 5.1 Inputs` and `## 5.2 Outputs`). Append to the existing paragraph, keeping it in the same section:

   ```
   ...; `advisory` (optional task-frontmatter block carrying one externally-supplied advisory — `id`, `package`, `fixed_version`, `source` — validated in Go and admitted into the planning findings table as a row labelled `external:<source>`, and independently re-verified at ai_review against the branch's module graph)
   ```

   The §5.1 range must end up containing `advisory`.

5. **Create `## Unreleased` in `CHANGELOG.md`** above the existing `## v0.17.13` heading (the file currently starts at a released section, so the heading does not exist yet — create it, do not append to a released section). Under it, one bullet:

   ```
   - feat: ingest one externally-supplied advisory from the task's `advisory` frontmatter — planning validates it in Go and admits it into the findings table as a row labelled `external:<source>` (deduped against scanner rows by non-empty fixed version), and ai_review verifies the advisory's package resolves to an installed version at or above its `fixed_version` in the branch's module graph, failing closed when the version is undeterminable
   ```

   Leave the rest of the file untouched.

6. **Before you finish**, first confirm both prompts this one documents have shipped: `pkg/advisory.go` must exist carrying `parseAdvisoryBlock`/`AdvisoryBlock`, and `pkg/steps_review.go` must carry `func (s *reviewStep) checkAdvisories`. If either is missing, STOP — write nothing, do not re-implement or stub the code, and report the run as failed with the explicit message `external advisory ingestion not yet deployed (prompts 1-2)`. Then re-run `<verification>` and confirm every command produces the expected output, then walk spec 007's Acceptance Criteria AC9 and AC10 against the change and confirm each one is satisfied. Confirm the documented behavior matches the shipped code — read `pkg/advisory.go`'s rejection messages and `pkg/steps_review.go`'s `checkAdvisories` notes and check that every claim you wrote is true of them. Confirm you did not add a `|` inside either extended table cell, and that neither extended row was wrapped onto a second line.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- This is a documentation-only change: do NOT modify any `.go` file, and do NOT touch `pkg/`, `main.go`, `cmd/`, or `pkg/prompts/`.
- The two extended table rows must stay single lines beginning with their current prefixes (`| Input | frontmatter \`repo\`` and ``| Side effects | `gh pr view ``) — the spec's acceptance evidence greps for exactly one line matching each prefix, and for that line containing `advisory` / `installed version` respectively.
- Do not introduce a second line anywhere in `docs/design.md` that begins with either of those prefixes.
- The `advisory` frontmatter block shape is frozen: a single mapping with the keys `id`, `package`, `fixed_version`, `source`. Do not document a list form, a second-advisory form, or any additional key as supported.
- Do NOT document a stdlib-specific version-resolution path — an advisory naming the Go standard library is not special-cased; the review fails closed and the note names the package.
- Do NOT change the `## Plan` / `## Result` / `## Review` JSON contracts or the `ReviewChecks` key names and meanings anywhere in the doc.
- The changelog entry needs a `feat:` prefix (this is new behavior → minor bump), one bullet, no version header, and must not describe verification steps.
- `make precommit` and `make test` stay green.
</constraints>

<verification>
Run `make precommit` — must exit 0.

```bash
grep -c '^| Input | frontmatter .repo.' docs/design.md
# expect: exactly 1

grep -n '^| Input | frontmatter .repo.' docs/design.md | grep -c 'advisory'
# expect: exactly 1

grep -c '^| Side effects | .gh pr view' docs/design.md
# expect: exactly 1

grep -n '^| Side effects | .gh pr view' docs/design.md | grep -c 'installed version'
# expect: exactly 1

sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'advisory'
# expect: >= 1

sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'fixed_version'
# expect: >= 1

sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'CVE-'
# expect: >= 1

sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'GHSA-'
# expect: >= 1

sed -n '/^## 3.3 Task format/,/^## 3.4/p' docs/design.md | grep -c 'needs_input'
# expect: >= 1

sed -n '/^## 5.1 Inputs/,/^## 5.2/p' docs/design.md | grep -c 'advisory'
# expect: >= 1

sed -n '/^## Unreleased/,/^## v/p' CHANGELOG.md | grep -ci 'advisory'
# expect: >= 1

grep -c '^## Unreleased' CHANGELOG.md
# expect: exactly 1

grep -n '^## v0.17.13' CHANGELOG.md
# expect: the first released heading, now below ## Unreleased
```
</verification>
