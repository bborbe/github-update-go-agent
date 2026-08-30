---
status: verifying
approved: "2026-08-23T23:05:38Z"
generating: "2026-08-23T23:06:05Z"
prompted: "2026-08-23T23:14:58Z"
verifying: "2026-08-30T22:40:26Z"
branch: dark-factory/bug-ai-review-leaked-tag-and-merged-false-positive
---

# ai_review false-positives: leaked-tag on release history + MERGED-state rejection

## Summary

- The agent's ai_review step rejects a valid update PR in two legitimate situations, and the rejection loops the task forever, starving the fleet.
- First false positive: `checkNoNewTag` flags a remote tag pointing at *any* commit in the PR's ancestry — but the ancestry includes the repo's legitimate release history (tags on master), so a normal release tag is misreported as "a tag leaked from the update pipeline".
- Second false positive: `checkPR` requires the PR be OPEN; a PR that has already MERGED (the shipped success state) is rejected with "pr state is MERGED, expected OPEN" and re-files the task.
- The rejection publishes `status=failed`, the task re-files to `planning`, the executor re-admits it (cap 1), and the cycle repeats indefinitely — observed 5+ re-runs in ~35 min on bborbe/kafka-topic-resend.
- Fix: (1) only flag tags the update branch itself introduced (exclude tags reachable from the base branch), (2) treat an already-MERGED PR as shipped/completed, not a rejection.

## Problem

`github-update-go-agent` v0.12.4 (deployed to dev+prod) has an ai_review verification step that cannot tell legitimate release history from a leaked tag, and cannot reconcile a PR that already merged. On any repo whose history contains release tags, an otherwise-clean update PR is rejected, the task re-files to planning, and the executor (single job slot, `maxConcurrentJobs=1`) re-admits the same task endlessly — holding the slot and starving the other ~310 queued repos. This stalls the goal's unattended fleet update on the whole 1.27.0 tail. Vault-side parking does not break the loop: the executor reads task state from the controller, which re-admits on the agent's failed publish.

## Goal

The ai_review step accepts an update PR whose branch is clean even when (a) the PR's ancestry contains legitimate release tags on master, and (b) the PR has already been merged. A rejected review is reserved for genuinely anomalous states (a tag the update branch itself created, a PR in a surprising non-OPEN non-MERGED state, dirty gates). On a MERGED PR the review routes through the existing approved path (approved → human_review, where the operator closes the already-merged task) — there is no new verdict or phase; the key outcome is that no `status=failed` re-file fires. The fleet drain is no longer starved by a cycling task.

## Reproduction

Environment: `github-update-go-agent` v0.12.4 on nuke-prod, task `bc31180b-626b-59e9-bd44-c99fb57c49de` for bborbe/kafka-topic-resend.

1. A previous update run opened PR #7 (`fix/update-go-0974bc5`, single commit `f8b922c2` bumping `go 1.27.0`). The PR's base commit history contains release commit `6e16a948` ("release v0.1.3") — a legitimate tagged release on master.
2. ai_review runs `checkNoNewTag`: `branchCommits = RevList(ref)` where `ref` = the task's pinned base SHA `0974bc5` (the HEAD the task branched from). `LsRemoteTags` returns `v0.1.3 → 6e16a948`. Since `6e16a948` is reachable from the base (it is in `branchCommits`), the check flags:
   ```
   "remote tag points at branch commit 6e16a948 — a tag leaked from the update pipeline"
   ```
   → `checks.NoNewTag = false`, review rejected, `status=failed`.
3. Operator merges PR #7 manually (`8a5ba6b`). The task re-runs; now `checkPR` sees `state = MERGED` and flags:
   ```
   "pr state is MERGED, expected OPEN"
   ```
   plus the same leaked-tag note → review rejected again, `status=failed`.
4. Each failed publish re-files the task to `planning` (`trigger_count` stays 1), the executor re-admits it (`concurrency_admit` log line, cap 1), and a new pod spawns. Observed pods for the same task: `bc31180b-…-8t2r8`, `-ndds5`, `-fzg65`, `-t47fl` in ~35 min.

Raw evidence (prod pod `github-update-go-agent-bc31180b-20260823225009-t47fl`):

```
I0823 22:28:24.862698  steps_execution.go:582] execution: PR opened (target=ready) https://github.com/bborbe/kafka-topic-resend/pull/7 branch=fix/update-go-0974bc5
I0823 22:29:08.744729  steps_review.go:156] ai_review: rejected — remote tag points at branch commit 6e16a9487769f6c35345a833b073bf8bf17c2717 — a tag leaked from the update pipeline
{"Status":"failed","NextPhase":"","Message":"remote tag points at branch commit 6e16a9487769f6c35345a833b073bf8bf17c2717 — a tag leaked from the update pipeline"}
```

Second run:

```
"notes": "pr state is MERGED, expected OPEN; remote tag points at branch commit 6e16a9487769f6c35345a833b073bf8bf17c2717 — a tag leaked from the update pipeline"
pr state is MERGED, expected OPEN; remote tag points at branch commit 6e16a9487769f6c35345a833b073bf8bf17c2717 — a tag leaked from the update pipeline
```

dark-factory: `dark-factory status` (daemon, not applicable to this repo's agent) — reproduction is of the deployed agent binary.

## Expected vs Actual

**Expected** (per design intent of `checkNoNewTag` — "verifies the remote holds no tag pointing at any branch commit (git ls-remote --tags unchanged with respect to the branch work)"):

- A tag on a commit that is already reachable from the base branch (release history) is NOT a leak — the update branch did not create it. `checks.NoNewTag = true`.
- A PR that is already MERGED is the shipped success state — the review reports shipped/completed, not a rejection. `checks.PROpen` semantics for MERGED = acceptable.

**Actual**:

- `checkNoNewTag` (`pkg/steps_review.go:263-295`) builds `commitSet` from `RevList(ref)` where `ref` is the **base** HEAD — so every release tag on master history is in the set and flagged as a leak. It cannot distinguish "tag the branch introduced" from "tag already on master".
- `checkPR` (`pkg/steps_review.go:170-192`) rejects any non-OPEN state, including MERGED — conflating "PR in a state we didn't create" with "PR shipped".

## Why this is a bug

- `checkNoNewTag`'s doc comment ("unchanged with respect to the branch work") states the intent is to catch tags the *branch work* introduced — but the implementation checks tags against the whole reachable-from-base commit set, not the branch's own new commits. A tag on master history is not branch work. Contradicts the documented contract (`pkg/steps_review.go:261-262` and `docs/design.md:161-162`, which states "`git ls-remote --tags` shows no tag at branch commits").
- `checkPR`'s doc (`pkg/steps_review.go:163-169`) describes the check as verifying the PR is "in the state the agent created it in" — for a PR the agent already merged, MERGED *is* the created success state, not an anomaly. Rejecting it re-files a completed task.
- The failure loop (re-file → re-admit → re-run) holds the executor's single job slot and starves the fleet — a systemic consequence beyond a single wrong review verdict.

## Acceptance Criteria

- [ ] **AC1 — base-reachable tags are not flagged:** in a unit test of `checkNoNewTag`, a remote tag whose SHA is reachable from `ref` (i.e. an ancestor of the base) does NOT set a leak note and `checks.NoNewTag = true`. Evidence: `go test ./pkg -run TestCheckNoNewTag` passes, specific test row asserts zero "tag leaked" notes when the tag SHA ∈ `RevList(ref)`.
- [ ] **AC2 — branch-introduced tags still flagged:** a tag whose SHA is in the update branch's own new commits (not reachable from base) still sets the leak note and `checks.NoNewTag = false`. Evidence: same test suite, row asserts the note fires for a tag on a branch-only commit.
- [ ] **AC3 — MERGED PR is approved, not rejected:** `checkPR` on a PR with `state=MERGED` does not append "pr state is MERGED, expected OPEN"; the review verdict is approved (routing to human_review per existing contract — no `status=failed` re-file). Evidence: `go test ./pkg` row asserts zero "expected OPEN" note + verdict approved for MERGED state.
- [ ] **AC4 — non-OPEN non-MERGED still surfaces:** a PR in a genuinely surprising state (e.g. `CLOSED`) still appends "pr state is X, expected OPEN" (for states that are not shipped). Evidence: test row for `CLOSED` state asserts the note fires.
- [ ] **AC5 — no task re-file after legit merge:** `make test` green and the changed review logic, when exercised against the repro scenario (kafka-topic-resend PR merged), produces an approved verdict with no `status=failed` re-file. Evidence: `make test` exits 0; verification replay (Rung-2) shows the task in human_review (or closed) with no leaked-tag/MERGED note.
- [ ] **Post-Deploy (Rung-2):** the fixed agent on dev, re-running the kafka-topic-resend repro, does not re-file the task with the leaked-tag/MERGED notes — the task's `## Result` shows approved with no "tag leaked" / "expected OPEN" note, and the executor no longer re-admits the `bc31180b` task after the fix.
  - `deploy_check:` `kubectlnukedev -n dev get pod $(kubectlnukedev -n dev get pods --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1].metadata.name}' | grep update-go-agent) -o jsonpath='{.spec.containers[0].image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `v0.12.5`

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — lint / format / generate / test / addlicense clean
- `make test` — unit + integration suites pass, including the new `checkNoNewTag` (base-reachable vs branch-introduced) and `checkPR` (MERGED/CLOSED) rows
- `grep -n 'expected OPEN' pkg/steps_review.go` — the MERGED-state guard present
- `grep -n 'RevList' pkg/steps_review.go` — the base-reachability comparison present

### Operator-executable (runs on the host after PR merge, spec verification ladder)

- Fix PR merged + released (v0.12.5) + deployed to dev and prod — `kubectlnukedev -n dev get pod <update-go pod> -o jsonpath='{.spec.containers[0].image}'` returns `:v0.12.5` (and same on prod)
- Repro replay: re-run kafka-topic-resend through the fixed agent on dev → the task reaches shipped/completed, its `## Result` contains no "tag leaked" / "expected OPEN" note; `kubectlnukeprod -n prod logs <executor> | grep bc31180b` shows no re-admission after the fix
- `dark-factory` verification not applicable (agent deployed as a Kubernetes Job image)

## Desired Behavior

1. `checkNoNewTag` computes the set of commits the update branch introduced — e.g. `RevList(base..branch)` or equivalently commits reachable from the branch tip minus commits reachable from the base — and flags a remote tag ONLY when its SHA is in that branch-only set.
2. A remote tag pointing at a commit already reachable from the base (release history) produces no leak note; `checks.NoNewTag = true`.
3. A remote tag pointing at a branch-introduced commit still produces the leak note; `checks.NoNewTag = false`.
4. `checkPR` treats `state == MERGED` as the shipped success state: no "expected OPEN" note, verdict approved (existing approved → human_review routing), no `status=failed` re-file.
5. `checkPR` still surfaces genuinely surprising non-shipped states (e.g. `CLOSED`) with the existing "pr state is X, expected OPEN" note.
6. The review verdict on a clean-and-shipped PR is approved (not `status=failed`); the task routes to human_review and is closed by the operator, not re-filed.
7. Existing gate checks (gates green, vulns clear, changelog unreleased, no branch-introduced tag) behave exactly as before for genuine anomalies.

## Constraints

- `ReviewChecks` / `ReviewOutput` serialized keys (`pr_open`, `pr_draft`, `gate_green`, `vulns_clear`, `changelog_unreleased`, `no_new_tag`) keep their names and meanings — downstream consumers read them.
- The check contract remains fail-closed for genuine anomalies (a real branch-introduced leak still rejects; a genuinely surprising PR state still surfaces).
- `gh.ViewPR`, `GitOps.RevList`, `GitOps.LsRemoteTags` seams are unchanged (mock-regenerated via counterfeiter, `make generate`).
- `pkg/steps_review_test.go` Ginkgo suite style is followed; coverage ≥ 80% on changed functions.
- No config fields, no opt-out flags, no new tunables.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| `git ls-remote --tags` rate-limited / fails | existing note "ls-remote --tags failed" + `NoNewTag` stays false (fail-closed) | retry on next task run |
| `git rev-list` fails on the range | existing note "rev-list failed" + fail-closed | retry on next task run |
| Base SHA not an ancestor of branch tip (force-pushed branch) | `RevList(base..branch)` yields an empty/invalid range → treat as non-fatal, keep current behavior | operator inspects task; re-drive |
| PR transitions OPEN → MERGED between `ViewPR` and review write | MERGED accepted as shipped (AC3) — no race window on rejection | task completes as shipped |
| Regression: a genuine branch-introduced tag after the fix | still rejected (AC2) — no scope change to the leak gate | existing escalation path |

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Fix `checkNoNewTag`: base-reachable tags excluded, branch-only tags flagged, unit tests | 1, 2, 3, 7 | AC1, AC2 | — |
| 2 | Fix `checkPR`: MERGED = approved, CLOSED still surfaced, unit tests; update `docs/design.md` no_new_tag row | 4, 5, 6, 7 | AC3, AC4 | — |
| 3 | Release + deploy: CHANGELOG, release v0.12.5, deploy dev+prod, Rung-2 verification | — | AC5 | prompts 1-2 |

Rationale: prompts 1 and 2 are independent single-function fixes in the same file; prompt 3 ships and verifies the combined result.

## Do-Nothing Option

The bug stays: every release-tagged repo that reaches a clean merge is rejected, re-files, and cycles — holding the single executor slot and starving the ~310-repo fleet drain indefinitely. The goal's unattended fleet update remains blocked on the whole release-tag-in-history set. Vault parking does not break the loop. The cost of not fixing is unbounded stalled drain plus manual per-repo merges.
