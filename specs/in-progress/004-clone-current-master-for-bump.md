---
status: verifying
approved: "2026-08-23T19:28:08Z"
generating: "2026-08-23T19:40:05Z"
verifying: "2026-08-23T20:46:58Z"
branch: dark-factory/clone-current-master-for-bump
---

## Summary

- The update-go agent plans against the task's pinned repo SHA (frontmatter `ref`) and gates the bump with the repo's own `make precommit`.
- With the one-at-a-time queue, tasks filed days earlier execute against a stale SHA whose repo tooling (e.g. golangci-lint v2.11.4) predates Go 1.27 support — the go 1.27.0 bump fails the gate with an export-data mismatch (`export data version 4 is greater than maximum supported version 2`).
- Fix: at planning time, resolve the clone ref to the repo's current default-branch HEAD, so the bump + precommit gate always run against current tooling.
- Self-healing: a task executed days late bumps the current master state; repos whose master already carries the target Go version are skipped as `go_current`.

## Problem

`github-update-go-watcher` files each "Update Go <repo> <sha>" task against the repo's HEAD SHA *at filing time*. The executor runs one task at a time, so a task filed 2026-08-16…08-20 can execute days later. The agent clones the pinned SHA and gates the repo's own `make precommit` there. If the repo upgraded its tooling after the SHA was pinned (sentry adopted golangci-lint v2.13.1 on 2026-08-21), the stale checkout still runs the old tool (v2.11.4), which cannot typecheck a `go 1.27.0` target:

```
could not import io ... could not load export data: internal error in importing
"internal/goarch" (cannot decode "internal/goarch", export data version 4 is
greater than maximum supported version 2)
```

Verified live 2026-08-23 on `bborbe/sentry`: task `Update Go bborbe-sentry 0da10df` (filed 2026-08-17) failed the gate; sentry PR #11 from a newer-SHA task (golangci v2.13.1) is approved + CI green — proving current tooling handles the bump. Fleet sizing: all ~40 in-progress tasks pin SHAs from 2026-08-16…08-20, so the whole 1.27.0 tail risks the same failure when each runs.

## Goal

A task's planning + bump + precommit gate runs against the repo's **current** default-branch HEAD at run start, not the (possibly stale) pinned SHA. Resolution happens at the start of the same run, so the resolved SHA cannot lag within the run (the lag this fixes is filing→run, not planning→execution). The pinned SHA stays in the task title/frontmatter for provenance, but the worktree the agent gates is current master — so the repo's current tooling (golangci v2.13.1 and later) is always what validates the go 1.27.0 bump.

## Non-goals

- Not changing the gate's contract: the agent still runs the repo's own `make precommit` (the repo's tools validate its own code) — only the checkout the gate runs on changes.
- Not changing the watcher's task filing or its `sha_unchanged` dedupe (task titles still carry the filing SHA).
- Not re-driving or re-filing the existing stale task queue in this change (operator/fleet action, separate).
- Not modifying the pinned-ref semantics for the review step's master comparison (the review step already reads `origin/master`).
- Not touching the false-env-claim or PR-merged-race failure modes.

## Assumptions

- The default branch of every in-scope repo is `master` (matching the watcher's existing scan and the review step's `origin/master` reads).
- `git ls-remote`/fetch of the default branch works under the agent's GitHub App installation token (the same credential already used for clone+push).
- Current repo tooling accepts go 1.27.0 — evidenced by sentry PR #11 being approved + CI green on golangci v2.13.1; repos whose tooling still cannot are out of scope of this change (their own tooling upgrade).

## Do-Nothing Option

If nothing changes, every queued "Update Go" task pinned to a pre-Go-1.27-tooling SHA fails its precommit gate when executed — the 1.27.0 tail of the fleet stalls with a non-obvious export-data error, each task needs manual re-filing against current master, and the goal's unattended-update headline keeps degrading on every future Go release (the queue always lags tooling upgrades). A manual re-drive of the current tail would clear today's backlog but recur each release. Doing the work is cheaper than the recurring operator tax.

## Failure Modes

| Mode | Effect | Detection / Recovery |
|---|---|---|
| Current-HEAD resolution fails (network/auth/rate-limit) | Task cannot pick a base to bump | Run reports a `failed` result naming the resolution step; re-run later succeeds |
| Default branch is not `master` | Wrong branch resolved | `ls-remote` resolves the actual default branch (`refs/heads/HEAD` symref) or the run fails loud naming the branch mismatch |
| Repo master already at target Go version | Redundant bump | Existing inspection classifies `go_current` → task closes cleanly (no change needed) |
| Repo tooling still pre-1.27 on current master | Gate fails again despite current checkout | Same export-data gate failure — repo needs its own tooling upgrade; task `failed` with the gate output |

## Constraints

- Must not regress: the gate's contract (repo's own `make precommit`), the watcher's filing + `sha_unchanged` dedupe, the task title/frontmatter provenance (`ref` stays recorded), and the review step's `origin/master` comparison (documented clone-at-ref semantics in `docs/design.md` become stale and must be updated).
- The resolution must not silently proceed on the stale pinned ref — a resolution failure stops/escalates (see Failure Modes).
- No new config fields, no opt-out flags, no tunable thresholds.

## Security / Abuse

The change performs a network read (`git ls-remote`/fetch of the default branch) with the agent's App installation token and reads the task's frontmatter `ref`. The resolved ref is used only as a clone base; no new token scopes or credential handling. A malicious `ref` value cannot redirect the fetch (the remote URL and branch are fixed); the frontmatter read is unchanged from today. Resolution errors surface loudly and never fall back to executing on an unverified base.

## Acceptance Criteria

- [ ] At planning time the agent resolves the clone ref to the repo's current default-branch HEAD (e.g. `git ls-remote`/fetch of the default branch) instead of the task's pinned `ref` — evidence: on a task whose pinned ref is stale (older than current master), the worktree HEAD equals the current default-branch HEAD at execution time, not the pinned SHA.
- [ ] The precommit gate runs against the current-default-branch checkout — evidence: re-running the sentry scenario (stale pinned ref + repo now on golangci v2.13.1) yields a green precommit gate on the go 1.27.0 bump, where the pinned-SHA run previously failed with the export-data mismatch.
- [ ] A repo whose current default branch already carries the target Go version is skipped as `go_current` (no redundant bump, no failed gate) — evidence: plan outcome `go_current` for such a repo.
- [ ] The task title + frontmatter still record the original filing SHA (traceability preserved) — evidence: `grep -n '^# '` on the task file shows the same title before and after the run (`git diff` on the task file is empty apart from the agent's own sections).
- [ ] When resolving current HEAD fails (network/auth/API error), the run does **not** proceed on the stale pinned ref — evidence: the run reports a `failed` (or `needs_input`) result whose message names the resolution step and the error; no bump/PR is produced on the stale base.
- [ ] `make precommit` exits 0 and `make test` exits 0 — evidence: both exit 0.

## Verification

### Container-executable

- Unit: the ref-resolution behavior is exercised via the existing GitOps fake seam — a fake returns a current HEAD; assert the planning/gate uses it and the pinned `ref` is only recorded (not used as the clone base); a fake resolution failure asserts the `failed`/`needs_input` path without a fallback.
- `make precommit` exits 0; `make test` exits 0.

### Operator-executable

- E2E (dev stage): queue a stale task against a repo that has since upgraded tooling (sentry is the natural candidate); confirm the bump + gate pass and a PR is opened, or the repo is classified `go_current` and skipped — evidence: task result + PR link.
- Fleet re-scan: 0 agent runs failing the gate on the export-data mismatch; go.mod distribution re-check shows the 1.27.0 tail draining.

## Desired Behavior

1. Planning reads `ref` from frontmatter for provenance (unchanged).
2. Planning resolves the repo's current default-branch HEAD at run start and clones that rather than the task's pinned `ref`.
3. On resolution failure, the run stops/escalates — a `failed` (or `needs_input`) result names the resolution step and the error; it never proceeds on the stale pinned ref.
4. All downstream steps (preflight, inspection, gate, execution) operate on the current-default-branch worktree exactly as before.
5. Repos already at the target Go version on the current default branch are classified `go_current` by the existing inspection — no behavior change needed there.

## Suggested Decomposition

| Prompt | Scope | Covers |
|---|---|---|
| 1 | Resolution + clone-at-current-HEAD + failure path (stop/escalate, no fallback) + unit tests via the GitOps fake seam | DB 2–3; AC 1, 2, 4, 5, 6 |
| 2 | Update `docs/design.md` clone-at-ref semantics (lines 92–93, 132, 147) to the run-start-resolution behavior | Constraints (docs/design.md) |
