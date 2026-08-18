---
status: verifying
approved: "2026-08-18T19:31:42Z"
generating: "2026-08-18T19:34:08Z"
prompted: "2026-08-18T19:59:52Z"
verifying: "2026-08-18T20:14:30Z"
branch: dark-factory/human-review-operator-action-block
---

## Summary

- The update-go agent parks successful runs in `phase: human_review` with the PR link buried in the `## Result` JSON block; the operator must parse JSON to find the PR and decide.
- Add a `## Your Move` operator-action block at the very top of the task body (above `## Plan`) when the agent routes to `human_review`.
- The block names the PR as a clickable markdown link, the action to take, and what changed — all in plain text, no JSON.
- The operator can open the task and act (merge) without reading any JSON section.
- Fixes the 2026-08-11 fleet evidence: 9 tasks parked in `human_review` whose bodies did not tell the operator what to do.

## Problem

`github-update-go-agent` writes `## Plan`, `## Result`, and `## Review` as typed JSON sections. When `ai_review` approves, the task reaches `human_review` with a body whose actionable fact — the PR URL — sits inside the `## Result` JSON (`pr_url`). A human operator opening the task must read raw JSON to find the PR, then infer what to do with it. At fleet scale (~44 repos/sweep) this is a recurring manual tax, and a misreading of the body has already misled a human operator (2026-08-11, `Update Go bborbe-kafka 207b734`).

## Goal

A task routed to `human_review` opens with a `## Your Move` section that a human can act on without reading any JSON: it states the PR as a clickable link, the single action the operator owns (merge the PR), and what the change consists of (Go version bump and/or dependency/vulnerability updates). The body's existing `## Plan` / `## Result` / `## Review` sections remain unchanged and continue to carry the machine-readable contract.

## Non-goals

- Not changing what the operator decides — the merge decision stays with the human.
- Not modifying the `## Plan` / `## Result` / `## Review` section formats or their JSON contracts.
- Not changing the `ai_review` checks, the `PR_TARGET` handling, or the draft→ready ordering (already resolved via stage-wide `PR_TARGET=ready`).
- Not writing the block on failure paths (`Failed`, `needs_input`).
- Not fixing the misleading Go-version header (owned by `github-update-go-watcher`) or the stale `## Failure` suppression (owned by `github.com/bborbe/agent`) — both split to their own tasks.

## Acceptance Criteria

- [ ] On `ai_review` approval routing to `human_review`, the task body opens with a `## Your Move` section whose line number is lower than the `## Plan` line — evidence: `grep -n "^## Your Move"` and `grep -n "^## Plan"` on a generated task body; the `## Your Move` line number is strictly smaller.
- [ ] The `## Your Move` block contains a clickable markdown PR link whose URL equals the `pr_url` value in the same body's `## Result` JSON — evidence: `grep -o 'https://github.com/[^)]*'` inside the block equals `pr_url` extracted from the `## Result` section.
- [ ] The `## Your Move` block states the operator action in imperative text — evidence: `grep -o 'Merge the PR'` on the block returns ≥1 match.
- [ ] The `## Your Move` block states what changed in plain text, derived from the plan/result: a Go version bump (`X → Y`) when `go_bump` is present, and/or dependency count and fixed-vulnerability IDs — evidence: for a Go-bump task the block contains the `go_bump.from` and `go_bump.to` values; for a vuln task it contains at least one `vulns_fixed` ID.
- [ ] The `## Your Move` block contains no JSON — evidence: the text between `## Your Move` and the next `## ` heading contains no `{` character.
- [ ] A task body on a non-human_review outcome (`Failed` / `needs_input`) contains no `## Your Move` — evidence: `grep -c "^## Your Move"` returns 0 on such a body.
- [ ] Re-running the review step on an already-human_review body does not duplicate the block — evidence: `grep -c "^## Your Move"` returns exactly 1 after a re-run.
- [ ] `make precommit` exits 0 and `make test` exits 0 — evidence: both commands exit 0.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — lint / typecheck / unit tests exit 0
- `make test` — full test suite exits 0
- Unit test asserts: a `human_review`-routed `ReviewOutput` renders a `## Your Move` block with link + action + change summary, and a rejected `ReviewOutput` renders no block
- Golden-body grep on the test fixture: `## Your Move` precedes `## Plan`

### Operator-executable (runs on the host after PR merge, spec verification ladder)

- Deploy to dev (image bump to v0.6.0+ in the agent Helm chart repo `values-dev.yaml` — the update-go agent Config CR is Helm-managed there, not in this repo), trigger or reuse one real update-go job that reaches `human_review`; then `grep -n "^## Your Move" <taskfile>` returns a line number lower than the `## Plan` line, and opening the task lets the operator identify the PR and action without reading the `## Plan` / `## Result` / `## Review` JSON.

## Desired Behavior

1. When `ai_review` approves, the review step writes a `## Your Move` section into the task body before routing to `human_review`, positioned above the existing `## Plan` section.
2. The block renders the PR as a markdown clickable link using the same PR URL the `## Result` section reports (`pr_url`).
3. The block states the operator action as an imperative sentence: merge the PR.
4. The block summarizes what changed in plain text: the Go version bump (`from` → `to`) when `go_bump` is present; otherwise the dependency-update count and the fixed-vulnerability IDs from `vulns_fixed`.
5. The block is idempotent: a re-run replaces the existing `## Your Move` section rather than appending a second one.
6. The block is absent on `Failed` and `needs_input` outcomes — it exists only for the end-of-pipeline success handoff.

## Constraints

- The `## Plan` / `## Result` / `## Review` JSON sections keep their exact current shape; the new section must not break `agentlib.ExtractSection[PlanOutput|ResultOutput|ReviewOutput]` round-trips.
- The `## Your Move` heading is fixed (named in the task's `# Verification` section).
- The block body is plain text with a single markdown link — no JSON, no HTML.
- The PR URL is sourced from the agent's own `ResultOutput.PRURL`; the block never interpolates the PR body, description, or any repo-file content.
- The block is written only when the result routes to `human_review`.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `ResultOutput.PRURL` empty or non-http(s) | Block renders the "PR URL unavailable" placeholder with the action text; review still routes to `human_review` (note: a fully-empty PRURL is guarded earlier — `Run` fails closed before approval — so the placeholder is defense-in-depth for the reachable non-empty-but-malformed case) | Operator opens the repo's PR list; the `## Result` JSON still carries the branch name |
| Block inserted after `## Plan` (ordering regression) | AC1 fails (line-order grep) in verification | Re-run the review step / fix insertion point; unit test asserts ordering |
| Re-run appends a second `## Your Move` | AC7 fails (`grep -c` returns 2) | Re-run the review step and confirm `grep -c "^## Your Move"` returns exactly 1 (replace-not-append semantics) |
| `go_bump` and `vulns_fixed` both empty (edge: adopted/no-op run routed to human_review) | Block states only the action and link, with an explicit "no version bump recorded" line | Acceptable — operator still sees the PR and action |

## Security / Abuse Cases

The block interpolates only the agent's own `ResultOutput.PRURL` (validated as a URL before rendering) plus numeric/ID fields (`go_bump.from`/`go_bump.to`, `deps_updated`, `vulns_fixed` IDs). It never interpolates the PR body, description, or any repo-file content, and renders the link as a plain markdown link — no HTML. No untrusted input reaches the body through this block.

## Suggested Decomposition

Single-layer, single-behavior change (one new body section in the review step plus a unit test) — the prompt-creator may produce a single prompt.

## Do-Nothing Option

Without the block, the human_review handoff keeps burying the PR in `## Result` JSON; operators keep parsing JSON to act, and the fleet-scale operator-load goal stays unmet. The 2026-08-11 misread evidence remains a live risk on every parked task.
