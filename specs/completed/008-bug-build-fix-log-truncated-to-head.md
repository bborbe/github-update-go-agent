---
status: completed
approved: "2026-10-01T06:32:03Z"
generating: "2026-10-01T06:32:03Z"
prompted: "2026-10-01T06:46:46Z"
verifying: "2026-10-01T07:03:39Z"
completed: "2026-10-01T07:40:52Z"
branch: dark-factory/bug-build-fix-log-truncated-to-head
---

## Summary

- When build-fix planning gathers evidence about a red build, it keeps the **beginning** of the job log and discards the end.
- A CI job log opens with runner setup and checkout; the failing step's message is at the end. The part that is kept is therefore the part that says nothing about the failure.
- Measured on a real failing run: the log is 649 lines, the failure sits at line 619, and only the first 200 lines are kept.
- The helper that performs the cut documents the opposite behaviour — its own comment calls the result a "log tail".
- Effect: the agent escalates builds it was built to fix, reporting that the evidence was truncated before the error.

## Problem

The build-fix lane exists so a red build is classified and repaired without an operator. Its planning step assembles log evidence and hands it to the model; the model needs the *failing step's own output*, which is the only part that names a root cause.

The fetch keeps the head of the log instead. On the measured run the retained 200 lines are entirely runner provisioning and a partial tag fetch, while the `changelog-fold` guard's verdict — which states the defect and its repair in four plain lines — sits at line 619 and is discarded. The model is then asked to classify a build from evidence containing no failure, and correctly answers that it cannot.

This is why three escalations filed 2026-09-30 across three repos (`bborbe/git-rest`, `bborbe/node-skeleton`, `bborbe/claude-supervisor`) all carry the same reason shape: *"the log evidence is truncated before the actual failure message"*. Each links a genuine failing run; the agent simply cannot see enough of it to act.

The "completed ≠ landed" shape is worth recording because it misleads: PR #34 (merged 2026-08-23) *did* land the gh-first log fetch and is live in prod. It routes evidence through a pre-existing cap that was never that PR's subject. The merge is real; the cap is the defect.

## Goal

A build-fix planning run on a real failing build receives log evidence that contains the failing step's error output, so the diagnosis can classify a root cause instead of escalating on missing evidence.

## Reproduction

Reproduce on the host; no cluster access needed. The bug lives in this agent, not in dark-factory, so `dark-factory --version` is not part of the repro — the affected agent tag is pinned below instead.

```bash
# 1. The failing run is 649 lines long.
gh run view 36622143039 --repo bborbe/claude-supervisor --log-failed | wc -l
# observed: 649

# 2. The 200-line cap lands mid-fetch — the last line the agent receives.
gh run view 36622143039 --repo bborbe/claude-supervisor --log-failed | sed -n '200p'
# observed: changelog-fold / changelog-fold	UNKNOWN STEP	2026-09-29T19:51:09.3047935Z  * [new tag]         v0.47.0                -> v0.47.0

# 3. The actual failure — 419 lines past the cut, never received.
gh run view 36622143039 --repo bborbe/claude-supervisor --log-failed | sed -n '617,619p'
# observed:
#   FAIL: 69 folded bullet(s) across 205 released section(s) at v0.66.1.
#   Restore a `## Unreleased` section above the top released heading and move the
#   folded bullets into it. Do not move a bullet whose merge IS in that section's
#   own tag — that one shipped, and moving it duplicates the entry in the next
#   release.
#   ##[error]Process completed with exit code 1.
```

The truncation point is in source:

- `pkg/gh_cli.go:282` — `return truncateToLines(string(logOut), 200), nil`
- `pkg/gh_cli.go:285` — doc comment: *"truncateToLines bounds s to at most n lines (diagnosis-sized log **tail**)."*
- `pkg/gh_cli.go:291` — body: `return strings.Join(lines[:n], "\n") + "\n... (truncated)"` — keeps the **head**.
- `pkg/steps_fix_planning.go:174` — the only caller, inside `runDiagnosis`; the task-body fallback at line 180 fires only when gh returns **empty**, never when it returns a truncated non-empty string.

`truncateToLines` has exactly one caller, so the behaviour is contained to this path.

### Prod is affected

Observed 2026-10-01, before any change:

```bash
kubectlnukeprod -n prod get pods -l agent.benjamin-borbe.de/assignee=build-fix-agent \
  --sort-by=.metadata.creationTimestamp
# build-fix-agent-ba362590-20261001060623-s6nk2   0/1  Completed  18m
kubectlnukeprod -n prod get pods -l agent.benjamin-borbe.de/assignee=build-fix-agent \
  --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1:].spec.containers[0].image}'
# docker.prod.nuke.benjamin-borbe.de:443/bborbe/github-update-go-agent:v0.17.13
git show v0.17.13:pkg/gh_cli.go | grep -n 'truncateToLines'
# 282:	return truncateToLines(string(logOut), 200), nil
# 286:func truncateToLines(s string, n int) string {
git show v0.17.13:pkg/gh_cli.go | sed -n '291p'
# 	return strings.Join(lines[:n], "\n") + "\n... (truncated)"
```

The deployed tag carries the identical head-slice, and the gh-first fetch is live in it — the 2026-10-01T04:51Z `build-fix-agent` run logged `steps_fix_planning.go:177 build-fix planning: gh log fetch failed …`, proving the fetch path executes in prod.

## Expected vs Actual

**Expected** — per `pkg/gh_cli.go:285`'s own contract ("diagnosis-sized log tail") and `runDiagnosis`'s stated intent (`pkg/steps_fix_planning.go:166-171`, *"prefer `gh run view --log-failed` … the body's `## Failing Workflows` table carries run URLs and job names — metadata, not the actual error text"*): the evidence handed to the diagnosis model contains the failing step's error output.

**Actual** — the evidence is the log's first 200 lines: runner provisioning, checkout, and a partial tag fetch. The failing step's output is discarded whenever the log exceeds 200 lines, which every observed build-fix episode does.

## Why this is a bug

The helper documents one behaviour and implements its inverse. A CI job log's failure is at the tail by construction: `gh run view --log-failed` returns the failed job's log in execution order, so provisioning and checkout lead and the failing step's output trails. A head-truncated log is therefore the one slice guaranteed not to contain the root cause.

Nothing about the cap's *size* is wrong — a 649-line log is not too large to bound, and the 18-second runtime of the measured run rules out size as a cause. The defect is which **end** is retained.

## Acceptance Criteria

- [ ] The log bound retains the final `n` lines, not the first `n`, and the unit suite fails when that is reverted — the regression lock. Evidence: a spec over a synthetic 649-line log whose failing line sits at index 618 asserts the returned string contains that line.
  - `evidence:` `go test ./pkg/... -run TestSuite -count=1` exits 0; the assertion is `ContainSubstring` on the failing line's text. Revert `lines[len(lines)-n:]` to `lines[:n]` and the same command exits non-zero — both runs recorded in the PR.
- [ ] When lines are dropped, the returned evidence states how many, ahead of the retained tail. Evidence: the same suite asserts the marker names the dropped count (`449` for a 649-line input at `n=200`).
  - `evidence:` `ContainSubstring` assertion on the marker text, in the suite above.
- [ ] An input already within the bound is returned unchanged and carries no marker. The input is pre-trimmed in the fixture, since the helper returns `strings.TrimSpace(s)` on that path.
  - `evidence:` `Expect(got).To(Equal(in))` in the suite above.
- [ ] The output stays bounded: a 10 000-line input yields at most 201 lines (200 retained + the marker).
  - `evidence:` a line-count assertion in the suite above (`Expect(strings.Count(got, "\n")).To(BeNumerically("<=", 201))`).
- [ ] `make precommit` exits 0.
  - `evidence:` exit code 0 from `make precommit` at the repo root.
- [ ] `docs/design.md` gains the build-fix lane's first contract section — the file documents the update lane only today — recording three invariants: the retained end is the tail, the marker names the dropped count, and the bound stays at 200 lines.
  - `evidence:` `grep -c '^### .*[Ll]og evidence' docs/design.md` returns ≥ 1, and `grep -A 12 '^### .*[Ll]og evidence' docs/design.md | grep -cE 'tail|dropped|200'` returns ≥ 3.
- [ ] **Post-Deploy (Rung-3):** a build-fix planning Job on prod carries the released image.
  - `deploy_check:` `kubectlnukeprod -n prod get pods -l agent.benjamin-borbe.de/assignee=build-fix-agent --sort-by=.metadata.creationTimestamp -o jsonpath='{.items[-1:].spec.containers[0].image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `v0.18.4` — **set this to the actual shipped tag at prompt-generation time, once the release has cut.** Phase 0.5 compares it as a literal string with no semver awareness, so a stale value hard-refuses verification. Expected bump is a patch (`fix:` bullet) from `v0.18.3`.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — lint, vet, unit suite clean
- `go test ./pkg/... -run TestSuite -count=1` — the log-bound helper's suite passes
- `grep -n 'lines\[len(lines)-n:\]' pkg/gh_cli.go` — secondary sanity check only. The behaviour-level proof is AC1's suite; an equivalent implementation would fail this grep while behaving correctly.
- `grep -c 'diagnosis-sized log tail' docs/design.md` — the contract is recorded

### Operator-executable (runs on the host after PR merge, spec verification ladder)

- `gh run view 36622143039 --repo bborbe/claude-supervisor --log-failed | tail -200 | grep -c 'FAIL: 69 folded bullet'` — the slice the helper now retains carries the failure
- `BRANCH=dev make buca`, then a dev build-fix planning Job log shows the failing step's error rather than ending mid-fetch
- `BRANCH=prod make buca`, then `kubectlnukeprod -n prod logs job/<build-fix-agent-…>` on a subsequent run shows a non-`needs_input` verdict on truncated-evidence grounds

## Desired Behavior

1. The log bound retains the log's final `n` lines.
2. The retained evidence is prefixed by a marker naming how many earlier lines were dropped. *Justification:* the helper already emits a `"... (truncated)"` marker on this path, so this refines existing behaviour rather than adding a surface — and without the count the model cannot tell whether evidence is missing from the head or the tail, which is the exact confusion this bug produced.
3. An input already within the bound is returned unchanged and carries no marker.
4. Build-fix planning on a run whose failure is at the log's tail receives that failure's text in the evidence block.
5. The evidence stays bounded — the cap is retained, not removed, so a very large log cannot inflate the diagnosis prompt.
6. The log-evidence contract is recorded in `docs/design.md`, which today documents the update lane only.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Retain the tail and name the dropped count in `pkg/gh_cli.go`, with its regression lock | 1, 2, 3, 4, 5 | 1, 2, 3, 4, 5 | — |
| 2 | Record the log-evidence contract in `docs/design.md` | 6 | 6 | — |
| 3 | Post-deploy prod gate | — | 7 | prompt 1 (needs the merge + release) |

Rationale: prompt 1 carries the entire code change and its regression lock, so prompt 2 is independent of it; prompt 3 can only run after the merge and the release cut.

## Constraints

- `truncateToLines` keeps its single caller and its signature; the code change is confined to `pkg/gh_cli.go`.
- The bound stays at 200 lines — this spec does not change the cap's size.
- `runDiagnosis`'s body-evidence fallback (`pkg/steps_fix_planning.go:180`) keeps firing only on **empty** gh output. Widening it to also fire on truncated output is out of scope here.
- No change to the `gh` invocation itself (`gh run view <id> --repo <repo> --log-failed`).
- The `docs/design.md` line is a doc-only edit and rides along in this PR.
- The repo's gate (`make precommit`) and existing suites stay green.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| Fetched log is shorter than the bound | Returned verbatim, no marker | None — correct by construction |
| Fetched log is empty | Helper returns empty; `runDiagnosis` falls through to task-body evidence (unchanged) | None — existing path |
| `gh run view --log-failed` exits non-zero — including GitHub API rate limiting | `FetchFailedLogs` returns a wrapped error; planning logs it and falls through to body evidence | Out of scope — see the sibling fetch-failure defect under Related. Detection artifact: the log line `build-fix planning: gh log fetch failed repo=… err=…` |
| Log's failure sits *earlier* than the final 200 lines | The head is discarded, so a failure in the head is lost | Accepted limitation — `--log-failed` returns the failing job in execution order, so the failure trails |

## Do-Nothing Option

Build-fix escalations keep arriving as `needs_input` on "truncated evidence" for builds the lane exists to repair. Each one costs an operator triage pass to establish that the build was genuinely red and the agent simply could not read it — the exact work the lane was built to remove. Three such escalations were filed in a single day.

## Related

- Task: `Build-Fix Agent Receives Truncated CI Logs and Cannot Classify Root Cause`
- `docs/design.md` — documents the **update** lane's pipeline only; the build-fix lane (`pkg/steps_fix_*.go`) is undocumented anywhere under `docs/`. This spec adds the build-fix lane's first contract section there.
- Sibling defect, **not** covered here: `gh run view --log-failed` can exit non-zero (observed `exit status 1` on `bborbe/ip`, run `21951103311`), after which `logEvidence` is empty and planning falls through to task-body evidence. A fetch *error* and thin *evidence* are indistinguishable downstream; both reach the same `needs_input`-prone path. Worth its own spec.
- Prior art: PR #34 (2026-08-23) added the gh-first fetch and `## Error` heading match; `truncateToLines` predates it (commit `7cec72b`, v0.12.0).
