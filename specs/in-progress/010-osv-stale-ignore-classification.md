---
status: approved
approved: "2026-11-24T07:46:22Z"
branch: dark-factory/osv-stale-ignore-classification
---

## Summary

- The planning phase already reads every operator-approved vulnerability ignore, but only to filter findings — nothing ever decides whether an ignore is still true.
- This change classifies every ignored advisory against the OSV database during planning, and removes the provably stale ones from all three ignore surfaces inside the same update pull request.
- An ignore is stale when its advisory is withdrawn, when the advisory's Go package is not in the repo's module graph, or when the repo's in-use version is outside every affected range.
- A CVE or GHSA alias whose advisory was merged into a `GO-*` record is resolved through the Go vulnerability index before it may be called legitimate — OSV answers 404 for a merged alias, and a recorded 404 is not evidence of legitimacy.
- Every ignore is recorded with one of three outcomes — stale, legitimate, unresolved — and only a stale outcome may be removed; an advisory the classifier cannot resolve is left in place.
- Removal is deterministic Go code, not a model instruction, so it is testable and cannot be skipped by the model.
- Stale-ignore removal counts as work under every update scope, so a repo whose only remaining task is a dead ignore still produces a pull request.

## Problem

`loadSuppressedVulnIDs` in `pkg/scanner_table.go` reads `VULNCHECK_IGNORE`, `.trivyignore` and `.osv-scanner.toml` only to drop suppressed IDs from the findings table. No code path ever asks whether a suppression still describes a real, still-applicable vulnerability, so a withdrawn advisory or an advisory for a package that has since left the module graph stays ignored forever — and every future gate run silently keeps excusing it.

The gap is measurable, not theoretical. `CVE-2026-33817` sat in both `bborbe/run` and `bborbe/ip` `.trivyignore` as the CVE alias of the withdrawn `GO-2026-4923`. `https://api.osv.dev/v1/vulns/CVE-2026-33817` answers 404 because OSV merged that alias into the `GO-2026-4923` record, so a classifier that queries only the recorded ID sees "not in OSV" and leaves the entry in place. Only the `GO-*` (or `GHSA-*`) lookup identifies the advisory as withdrawn. A manual sweep found it; the automatic path as originally conceived would have missed it.

## Goal

Every planning run that reaches the inspection phase classifies every ID in the repository's three ignore surfaces against OSV and records each classification on the task's `## Plan` section with its outcome. The execution phase removes every ID whose outcome is stale from every surface it appears on — `VULNCHECK_IGNORE` in `Makefile` and `Makefile.precommit`, `.trivyignore`, and `.osv-scanner.toml` — and the resulting pull request contains those removals. IDs classified legitimate or unresolved are left untouched. A repository whose only remaining work is a stale ignore still produces a pull request.

## Non-goals

- Adding new suppressions. Suppression remains a human risk call (design D4); this change only removes entries the OSV data proves are dead.
- Re-auditing legitimate no-fix ignores for risk or reachability beyond the three staleness reasons above.
- Changing how findings are filtered or reported in `pkg/scanner_table.go`, or how the model classifies fix-versus-park.
- Emitting a task when only stale ignores exist — that belongs to the watcher-side task.
- A standalone removal script for operators; removal runs inside the agent's execution phase.
- Repositories whose `.maintainer.yaml` opts out of `goUpdate.autoUpdate`, or that never reach inspection (no gate target, a broken gate, a hardcoded CI Go pin): their ignore surfaces are never read, so their dead ignores stay until the repo opts in or the precondition is fixed.
- Any change to the gate targets or to the scanner output parsing.

## Assumptions

- The agent pod can reach `api.osv.dev` and `vuln.go.dev`. This is verified, not assumed: `bborbe/nuke`'s `egress-proxy` allowlist carries `.osv.dev` and `.vuln.go.dev` in its own right (`egress-proxy/egress-proxy-deploy.yaml`), because `osv-scanner` and `govulncheck` already query them as gate targets.
- The repository's `go.mod` is the source of the module graph. A module under a `replace` that resolves to a local path, or a require version that an `exclude` removes, has no resolvable effective version and is classified unresolved rather than stale.
- The YOLO container has `git` and can run `git init` in a temporary directory; the repository's own `.git` is masked by `workflow: worktree`, which does not affect a fresh temp directory.
- The YOLO container can run this repo's `make precommit`. Verified in the image the daemon runs (`docker.io/bborbe/claude-yolo:v0.16.4`): `trivy`, `git` and `make` are all present, and prior prompts on this repo ran `make precommit` as their verification. `trivy`'s database download needs `ghcr.io` egress, which the container's proxy provides for the gate targets this repo already runs.

## Acceptance Criteria

- [ ] **The classifier maps every input class to exactly one outcome, with a test per class** — `ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='OSV|StaleIgnore' -ginkgo.v` exits 0 and its output reports `Ran N of` with N ≥ 10. The focused specs assert a concrete `(id, outcome, reason)` triple for each of: withdrawn record; alias-resolved withdrawn record (the queried CVE 404s and the Go index maps it to a withdrawn `GO-*` record); package absent from `go.mod`; in-use version outside every affected range; in-use version inside an affected range; `stdlib` advisory never package-absent; a module under a local-path `replace` (unresolved); a require version removed by `exclude` (unresolved); 404 with no alias (unresolved); transport error (unresolved). Evidence: the command's exit code, the `Ran N of` line, and the per-case assertions.
- [ ] **`make precommit` exits 0** on the changed tree. Evidence: exit code and the `ready to commit` line.
- [ ] **Planning writes every classification onto `## Plan`** — driving the planning step with a fake Claude runner and a fake OSV client over a seeded workdir writes a `## Plan` section whose `ignore_classifications` carries the withdrawn ID with outcome `stale`, the still-applicable ID with outcome `legitimate`, and the unknown ID with outcome `unresolved`, each with a reason. A model-supplied `ignore_classifications` value is discarded and replaced by the Go result. Evidence: the asserted section content.
- [ ] **A plan whose only work is stale-ignore removal routes to execution** — `shouldClose` returns false for that plan under `both`, `golang` and `deps`. Evidence: the three asserted return values.
- [ ] **Execution removes a stale ID from all three surfaces and leaves a legitimate ID untouched** — an integration test seeds a temporary git repository (fresh `git init`, pinned committer identity) whose `Makefile` `VULNCHECK_IGNORE` (with a `\` continuation line), `.trivyignore` and `.osv-scanner.toml` each carry the stale ID and the legitimate ID; after removal `git diff` shows the stale ID absent from all three files, the legitimate ID present on all three, the `.osv-scanner.toml` still parseable with its other `[[IgnoredVulns]]` entries byte-identical, and the test prints the diff. Evidence: the assertions and the printed diff.
- [ ] **An unresolvable advisory is never removed and an empty list changes nothing** — with the stale list empty, all three files are byte-identical; with only unresolved and legitimate entries, the removal function removes nothing; a transport error during classification does not fail the planning run. Evidence: byte comparison and the returned removed-set.
- [ ] **The pull request body lists the IDs actually removed** — the removal function returns the IDs it deleted; the PR body builder renders those, and a plan whose stale ID is already absent yields a body with no removal line. Evidence: unit test on the builder with both inputs.
- [ ] **The gate runs on the cleaned tree** — a fake gate runner that records the file content it is invoked against sees the stale ID already gone, proving removal precedes the gate run. Evidence: the recorded content assertion.
- [ ] **`docs/design.md` records the removal exception** — `grep -n 'stale ignore' docs/design.md` returns ≥ 1 line, and D4 plus the "Suppression surfaces" section no longer claim the agent never writes the ignore surfaces. Evidence: the grep and the section text.

## Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `ROOTDIR=/workspace make precommit` — ensure, format, generate, test, check and addlicense all pass
- `ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='OSV|StaleIgnore' -ginkgo.v` — the classifier and removal specs pass, `Ran N of` with N ≥ 10
- `grep -n 'ignore_classifications' pkg/plan_output.go` — the plan field exists (secondary sanity check only)
- `grep -n 'removeStaleIgnores' pkg/steps_execution.go` — execution calls the removal (secondary sanity check only)

## Operator-executable (runs on the host after PR merge)

- `TASK_FILE=<seeded task> go run ./cmd/run-task --phase planning` against a repository seeded with a withdrawn ignore, followed by the execution phase, opens a pull request whose diff removes that ID from every surface it appears on (the task's seeded-repo exercise).

## Desired Behavior

1. During planning, after the three ignore surfaces are read, Go classifies each distinct ignored ID against OSV before the plan is written. The planning model is never asked to classify an ignore.
2. Each ID receives exactly one outcome: `stale`, `legitimate`, or `unresolved`.
3. Stale with reason `withdrawn` when the advisory record's `withdrawn` field is set.
4. Stale with reason `package-absent` when the advisory names a Go package absent from the repository's module graph. A `stdlib` advisory is never package-absent: the Go toolchain directive governs it, so its affected ranges are checked against the directive's version.
5. Stale with reason `version-not-affected` when the advisory's affected ranges do not contain the repository's in-use version of that module.
6. Legitimate when the advisory affects the in-use version.
7. Presence and version come from the repository's `go.mod` with `replace` and `exclude` honoured. A module whose effective version cannot be resolved — a `replace` to a local path, or a require version removed by `exclude` — is `unresolved`, never stale.
8. When the direct lookup answers 404, the classifier resolves the ID through its `GO-*`/`GHSA-*` alias before concluding anything: it consults `https://vuln.go.dev/index/vulns.json` lazily on the first 404, once per run, and follows the matching alias to the full record. Only when no alias exists is the ID `unresolved`.
9. Any transport, status or parse failure yields `unresolved`; it never yields stale and never aborts the planning run. Each request has a 10-second timeout and classification as a whole has a 60-second budget; an ID past the budget is `unresolved`.
10. The `## Plan` section records every ID with its outcome and reason. Go overwrites the field after parsing the model's plan, exactly as `gate_targets` is overwritten, so a model-emitted value cannot remove a suppression.
11. Stale-ignore removal counts as in-scope work under `both`, `golang` and `deps`; it is scope-independent.
12. Execution removes every `stale` ID from every surface it appears on, before the gate targets run, and returns the IDs it actually deleted. It re-checks the local conditions (package presence and in-use version against the execution workdir) and drops any ID that no longer qualifies; the `withdrawn` verdict is taken from the plan.
13. The pull request body lists the IDs actually removed, never the plan's intended list.

## Constraints

- The classifier must not require a new dependency; `golang.org/x/mod` (already required) supplies module-file parsing and semver comparison.
- The OSV lookups are `GET https://api.osv.dev/v1/vulns/<id>` and, on 404, the Go index at `https://vuln.go.dev/index/vulns.json`. Both hosts are already on the agent pod's egress allowlist (`.osv.dev`, `.vuln.go.dev`).
- The classifier must be injectable so tests use recorded responses and never the network. Follow the fetcher seam pattern in `pkg/maintainerconfig/fetcher.go` (interface plus an HTTP implementation).
- Removal must be idempotent and must touch only the files whose content changes.
- Surface-specific removal rules: a stale ID in `.osv-scanner.toml` removes its whole `[[IgnoredVulns]]` table (header, `id`, `reason`); a stale ID in `.trivyignore` removes its line and, when a comment block above it documents only that ID, the block; a stale ID in `VULNCHECK_IGNORE` removes the token and repairs or drops the continuation line, leaving the assignment in place even when the value becomes empty.
- Existing behaviour of `loadSuppressedVulnIDs` and the findings filter is unchanged.
- The execution model must not be asked to edit the ignore surfaces; Go performs the removal deterministically.
- `docs/design.md` D4 and the "Suppression surfaces" section are amended to record the removal exception, and `suppressionSurfacesHint` in `pkg/steps_planning.go` is updated so the park message no longer claims the agent never writes the surfaces.
- Do not commit `.github/workflows/**`.

## Failure Modes

| Trigger | Expected behavior | Recovery |
|---|---|---|
| OSV returns 5xx or times out for one ID | that ID is `unresolved`, stays in place, planning continues; the reason is recorded on `## Plan` | operator re-delegates the task; the next `## Plan` carries the ID with a resolved outcome |
| OSV rate-limits a repo with many ignores | requests past the 60-second budget are `unresolved` and stay in place; planning continues | operator re-delegates later; the ID is re-classified |
| The Go index cannot be fetched after a 404 | the ID is `unresolved`, stays in place; planning continues | operator re-delegates; no suppression is lost |
| A stale ID is still reported by a gate scanner (misclassification) | the gate runs on the cleaned tree and goes red; execution fails with the failing target and output tail | `## Result` shows `outcome: failed` with the failed target and the ID in the tail; the operator re-adds the line and re-delegates |
| The module graph changed between planning and execution | execution re-checks package presence and in-use version; an ID that no longer qualifies is left in place | none needed |
| Two runs on the same repo at different heads both remove the same line | the second run's removal finds nothing to change; its PR carries no ignore change for that ID | none needed |
| Two IDs share one `.trivyignore` comment block and only one is stale | only the stale ID's line is removed; the block and the other ID remain | none needed |
| The plan is replayed after the ID was already removed | removal finds nothing to change; the commit contains no ignore change | none needed |

## Security / Abuse

- The classifier only reads public advisory data; it sends no repository content to OSV. The requests are GET-by-ID; the Go index is fetched without repository identifiers.
- Removal is confined to the named ignore files at the repository root; a path traversal cannot occur because the filenames are constants joined to the workdir.
- A malicious OSV response cannot cause an arbitrary file write: classification only produces an ID-plus-outcome list, and removal only deletes known ID tokens from known files.
- The plan is agent-written and re-read at execution, so a tampered `## Plan` could name a legitimate ID. Execution therefore re-checks the local conditions against its own workdir and drops any ID that no longer qualifies; the residual risk is an ID whose `withdrawn` verdict was fabricated, which requires control of the task body.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | OSV classifier library: record types, HTTP seam, alias resolution through the Go index, go.mod graph with replace/exclude, version-affected math, the three-outcome set, timeouts | 1-9 | 1 | — |
| 2 | Planning integration: classify after loading suppressions, carry every classification on the plan, discard any model-supplied value, count stale-ignore removal as work under every scope | 10, 11 | 3, 4 | prompt 1 |
| 3 | Execution removal: remove stale IDs from all three surfaces before the gates, re-check local conditions, return the removed set, render it in the PR body, seeded-repo integration test, design.md amendment | 12, 13 | 2, 5, 6, 7, 8, 9 | prompt 2 |

Rationale: prompt 1 establishes the classification contract and its test fixtures; prompt 2 owns "the plan carries every outcome"; prompt 3 owns "removal happens, before the gates, from the actually-removed set". AC1 is cross-cutting: its classifier half lands in prompt 1 and its never-removed half in prompt 3.

## Do-Nothing Option

Every repository keeps suppressing advisories that no longer exist. Withdrawn records, advisories for packages long removed from `go.mod`, and advisories for versions the repo no longer uses all remain excused indefinitely. The failure is silent: the gate stays green because the suppression does the work, so nothing surfaces the dead entry until a human sweeps the files by hand — as happened for `CVE-2026-33817` in `bborbe/run` and `bborbe/ip`.
