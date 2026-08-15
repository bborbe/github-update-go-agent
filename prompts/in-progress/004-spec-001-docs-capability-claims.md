---
status: approved
spec: [001-configurable-pr-target]
created: "2026-08-15T09:10:00Z"
queued: "2026-08-15T08:16:38Z"
branch: dark-factory/configurable-pr-target
---

# Correct the never-ready capability claims and document the PR_TARGET setting

<summary>
- Every place the written record claims the agent can only ever open draft pull requests now describes the configurable behaviour instead.
- The README's opening paragraph, the review-output field comment, and three lines in the design document are corrected.
- The design document's capability-removal record gets a dated reversal note rather than a silent edit, because it records a decision that is being deliberately reversed.
- The prohibitions that still hold — no merging, and no changing the state of a pull request that already exists — are restated explicitly wherever the old claim is removed, so nothing reads as a wider capability grant than was made.
- The new deployment setting is documented in the README's environment-variable table and in the design document's inputs section, so an operator can discover it without reading the code.
- The design document's execution-phase descriptions stop implying the draft flag is always passed.
- No code behaviour changes in this prompt — comments, README and design document only.
- The default is stated everywhere as draft, so a reader knows an unconfigured deployment behaves as before.
</summary>

<objective>
Reconcile the written record with the shipped behaviour: every location asserting that the agent unconditionally opens draft pull requests must describe the per-deployment `PR_TARGET` choice instead, the design document's capability-removal record must carry a dated reversal marker, and the setting itself must be documented where an operator looks for it.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read fully before changing anything:
- `README.md` — the opening paragraph (line 3) and the `## Env Vars` table.
- `docs/design.md` — `## 3.5 Downstream consumers` (line 110), `## 4.3 Per-phase decisions` (the `execution` rows around lines 122, 147, 148), `## 5.1 Inputs`, `## 5.2 Outputs` (line 181), and `## 7.0 Consent gates (capability removal)` (lines 201, 203).
- `pkg/review_output.go` — the `PRDraft` field comment (line 13).
- `pkg/steps_review.go` — already corrected by prompt 3; read it to confirm and to reuse its wording for the target-match behaviour.
- `pkg/pr_target.go` and `main.go` — the authoritative names and values (`PR_TARGET`, `draft`, `ready`, default `draft`) so the documentation matches the code exactly.
- `/home/node/.claude/plugins/marketplaces/coding/docs/documentation-guide.md` and `/home/node/.claude/plugins/marketplaces/coding/docs/readme-guide.md` — documentation style.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — changelog entry style.

Run this first to see exactly which sites are still outstanding after prompts 1-3:

```bash
grep -rniE 'readies|never-ready|no ready/merge|--draft is hardcoded|never flips a draft' \
  --include='*.go' --include='*.md' . | grep -v vendor | grep -v '^specs/' | grep -v '^prompts/'
```
</context>

<requirements>
1. **`README.md` line 3** — the opening paragraph currently says the agent "lands a **draft PR**" and "never auto-suppresses, never tags, never readies or merges the PR". Rewrite it so that:
   - it says the agent lands a pull request, draft by default and ready for review when the deployment sets `PR_TARGET: ready`;
   - it still states the prohibitions that hold: the agent never auto-suppresses, never tags, never merges, and never changes the state of a pull request that already exists;
   - the token `readies` does not survive anywhere in the file.

2. **`README.md` `## Env Vars` table** — insert a new row directly after the `MODEL` row:

   ```
   | `PR_TARGET` | no | `draft` | `draft` or `ready` — how the agent opens pull requests. Unset behaves exactly as before: drafts only |
   ```

   Keep the existing column order (`Var | Required | Default | Purpose`).

3. **`pkg/review_output.go` line 13** — the `PRDraft` field comment currently reads "`gh pr view` reports isDraft true (the agent never readies)". Replace it with a comment stating that the field is the RAW observed draft-ness reported by `gh pr view`, that the review compares it against the configured `PR_TARGET`, and that this field always reports what was observed rather than the match verdict — the approval flag and the notes carry the match decision. Do NOT change the field name, its type or its `json:"pr_draft"` tag.

4. **`docs/design.md` line 110 (`## 3.5 Downstream consumers`)** — currently "Human reviews + readies the draft (runbook … step 3) → `pr-review-agent` (step 4) → …". Rewrite so the downstream flow describes both configurations: under the default draft target a human reviews and promotes the pull request (runbook step 3) before `pr-review-agent`; under `PR_TARGET: ready` the pull request is opened ready for review and reaches `pr-review-agent` without that step. Keep the rest of the chain (human merge step 5 → `github-releaser-agent` step 6) and the parked-task sentence unchanged.

5. **`docs/design.md` line 201 (`## 7.0`)** — the bullet ends "never-tag/never-push/never-ready is structural for the LLM, not prompt-enforced". Keep the never-tag and never-push claims and the "structural for the LLM, not prompt-enforced" framing; drop the never-ready claim and state instead that the pull-request target is a deployment setting resolved in Go before the LLM runs, so the LLM cannot influence it either way.

6. **`docs/design.md` line 203 (`## 7.0`)** — the bullet currently reads "No ready/merge: PR creation via `gh pr create --draft` only; no code path invokes `gh pr ready`/`gh pr merge` (mirrors dark-factory-agent)." Replace it with a bullet that:
   - states the agent still cannot merge and still cannot change the draft-ness of an already-open pull request — no code path invokes `gh pr ready` or `gh pr merge`;
   - states that the creation target is chosen once, at creation time, from `PR_TARGET` (`draft` default, `ready` opt-in);
   - carries the literal dated reversal marker `reversed 2026-08-15` naming what was reversed: the draft-only rule recorded here is now operator-configurable, while the merge and flip prohibitions stand.

   The marker is load-bearing: `grep -c 'reversed 2026-08' docs/design.md` must return at least 1. Use exactly the string `reversed 2026-08-15` — do not paraphrase it.

7. **`docs/design.md` `## 5.1 Inputs`** — append the deployment setting to the input list: `PR_TARGET` (`draft` default | `ready`), read from the Job environment, selecting the pull-request target at creation time.

8. **`docs/design.md` — the remaining `gh pr create --draft` / "draft PR" assertions.** These assert the same removed claim in different words, so the spec's AC9 grep does not catch them; correct them anyway so the design document does not contradict the code:
   - The execution rows (the `execution` phase row in `## 4.2 Supported phases` around line 122; the "Side effects" and "Allowed tools" rows in `## 4.3 Per-phase decisions` around lines 147-148): change each literal `gh pr create --draft` to `gh pr create` and append the parenthetical "`--draft` only when the target is draft".
   - `## 5.2 Outputs` ("Branch + draft PR on target repo"): change to "Branch + PR on target repo (draft by default; ready when `PR_TARGET: ready`)".
   - `## 4.3` Postconditions row ("draft PR open; …" around line 155) and the §6 change-summary line ("execution: draft PR open, …" around line 235): both become false under `PR_TARGET: ready` — reword to "PR open (draft by default; ready when `PR_TARGET: ready`)".
   - Leave every other occurrence of the word "draft" in `docs/design.md` alone — the frontmatter `status: draft`, the D1/D3 decision rows and the §1.4/§2.2 motivation prose all remain accurate under the default.

9. **Catch any straggler the grep still reports.** Prompts 2 and 3 own three of the eight sites (`pkg/gh_cli.go` ×2, `pkg/steps_review.go` ×1); the other five (`README.md`, `pkg/review_output.go`, `docs/design.md` ×3) are yours. Re-run the grep from `<context>`; if it reports anything outside your own site list, correct it here using the same framing as requirement 3 (observed draft-ness compared against the configured target, mismatch declines with a note). If it reports nothing beyond your sites, change nothing extra.

10. **Do not reintroduce the forbidden tokens.** None of the strings `readies`, `never-ready`, `no ready/merge`, `--draft is hardcoded`, or `never flips a draft` may appear in any new wording you write — including the `CHANGELOG.md` bullet in requirement 11. The spec's evidence grep scans `*.go` and `*.md` across the repo, so a changelog entry containing one of them would fail the criterion. Additionally, in `.go` files the prohibition restatements must use prose only ("no Ready, no Merge", as prompt 2's interface comment does) — never the literal strings `pr ready` or `pr merge`, which would fail the capability grep in the verification block below.

11. Append one bullet to the existing `## Unreleased` section of `CHANGELOG.md` (do not replace the section, do not add a version header):
    `- docs: README, design doc and review-output comments now describe the configurable PR_TARGET (draft default | ready) instead of asserting the agent only ever opens drafts; the design doc's §7.0 capability-removal record carries a dated reversal note, and the merge and flip prohibitions are restated explicitly`

<!-- Open question surfaced for the reviewer: the spec's AC9 evidence grep filters only `vendor` and `^specs/`. Since the spec was written, the pipeline's own prompt files landed under `prompts/`, and prompts 2 and 3 quote the old wording verbatim in their requirements. The verification below therefore adds `grep -v '^prompts/'`. Without that filter the command returns the prompt-file matches and can never reach 0 while the prompts remain in the working tree. The intent of AC9 — zero matches in shipped source and documentation — is unchanged. -->
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Documentation only: no behaviour change. The only `.go` edits permitted are the `PRDraft` field comment in `pkg/review_output.go` and comments in any file the `<context>` grep still reports (per requirement 9's generalized straggler scope). Do NOT change any identifier, signature, struct tag or code path.
- `pr_draft` in the review output keeps its current meaning — raw observed draft-ness. The documentation must say so; do not describe it as "matches the target".
- The agent must not be described as gaining the ability to merge a pull request, or to change the draft-ness of a pull request that already exists. Every corrected site must restate those prohibitions rather than merely dropping the old sentence.
- The default with no configuration present is draft and must be documented as such at every site that mentions the setting.
- Per-repository or per-task selection is out of scope and must not be implied anywhere in the documentation. The setting is per-deployment.
- Enabling `ready` in a cluster is a cross-repo Helm change (the agent `Config` CR's `spec.env`); do not claim this repo can set it, and do not add or edit any manifest here.
- Never `fmt.Errorf`; use `github.com/bborbe/errors` (not expected to come up in a docs-only prompt).
- Do NOT run `go mod vendor` and do NOT use `-mod=vendor`; this repo does not commit `vendor/`.
- Existing tests must still pass.
</constraints>

<verification>
Run `make precommit` — must exit 0.

```bash
grep -rniE 'readies|never-ready|no ready/merge|--draft is hardcoded|never flips a draft' \
  --include='*.go' --include='*.md' . | grep -v vendor | grep -v '^specs/' | grep -v '^prompts/'
# expect: 0 lines (spec AC9; the prompts/ filter is explained in the requirements note)

grep -c 'reversed 2026-08' docs/design.md
# expect: >= 1 — the §7.0 capability-removal record carries a dated reversal marker

grep -n 'PR_TARGET' README.md docs/design.md
# expect: the README env-vars row, the README opening paragraph, and the design doc's
#         §3.5, §5.1, §5.2 and §7.0 mentions

grep -rn 'pr ready\|pr merge' --include='*.go' . | grep -v vendor
# expect: 0 lines — no ready-flip or merge capability exists (spec AC8)

grep -c 'json:"pr_draft"' pkg/review_output.go
# expect: 1 — the json tag is unchanged (count the tag, not the name, so a comment
#         mentioning pr_draft does not break the count)
```
</verification>
