---
status: prompted
approved: "2026-11-26T07:49:30Z"
generating: "2026-11-26T09:01:12Z"
prompted: "2026-11-26T09:01:12Z"
branch: dark-factory/nightly-fleet-scan
---

## Summary

- Two always-on services scan the same `github.com/bborbe/*` fleet on their own clocks: `github-vuln-watcher` (StatefulSet, `POLL_INTERVAL=12h`) and the stale-ignore pass inside `github-update-go-watcher` (rides the 10-minute poll loop).
- This change adds one deterministic, LLM-free task type to the existing `github-update-go-agent` binary: `github-fleet-scan`. One nightly Job runs it once, scans the fleet, files the per-repo `github-update-go` tasks, and exits.
- The scan reuses the code that already ships in this binary — `OSVFetcher`, `IgnoreClassifier`, the GitHub client and the `go.mod` parser — and ports only the fleet orchestration the two watchers still own.
- The stale rule is the retired watcher's, not the broader in-PR classifier's: an ignore is stale only when OSV reports the advisory `withdrawn`. The three-condition verdict (withdrawn, package absent, in-use version unaffected) that `pkg/osv_classifier.go` implements for the update PR is deliberately **not** the removal trigger here.
- Filed tasks reuse the two watchers' frozen UUID5 namespaces verbatim, so the migration does not re-file a single open work item.

## Problem

The scan work is real and must keep happening, but both producers are long-lived pods that spend almost all their time idle. `github-vuln-watcher` holds 2 CPU / 8Gi of requests and a 40Gi `local-path` datadir between scans; its whole job is one pass every 12 hours. The stale-ignore pass has no schedule of its own at all: it rides the update watcher's poll loop, reads at most 10 repos per cycle, and exists only while that service runs. Neither can promise a fixed nightly run, and both are resources held for nothing between runs.

The consumer side already exists. `github-update-go-agent` owns the GitHub App client, the OSV fetcher, the `go.mod` parser and the `IgnoreClassifier`, and it already consumes the very `github-update-go` tasks these scans produce. What it does not have is a way to produce them.

## Goal

A `github-fleet-scan` task run by one Job scans the fleet once and files one `github-update-go` task per repo that needs a change — a firing vulnerability, a withdrawn-and-still-suppressed advisory, or both — then exits. A second run over an unchanged fleet files nothing. The run logs `scanned=N` and `filed=M`; a second run additionally logs `already_recorded=M2` with `N` and `M` unchanged.

## Non-goals

- The `Config` CR and the `Schedule` CR. They live in `bborbe/nuke` and are a separate manifest change; this spec covers the code only.
- Removing the `github-vuln-watcher` StatefulSet or deleting the stale-ignore pass from `github-update-go-watcher`. Both are follow-up changes gated on this one working in prod.
- Fixing the vulnerabilities the scan finds. The `github-update-go` lane does that from the filed tasks.
- Changing the in-PR stale-ignore classifier or its removal behaviour. The scan reuses `IgnoreClassifier` as a source of verdicts; it does not change what the update PR removes.
- Alerting on a missed night.
- Repos outside the configured allowlist.

## Assumptions

- The pod can reach `api.osv.dev` and `vuln.go.dev`; the update lane already queries them, and `bborbe/nuke`'s `egress-proxy` allowlist carries both.
- The pod can reach GitHub and mint the same App token the update lane already uses.
- The pod has `git` and the repo's gate targets (`govulncheck`, `trivy`, `osv-scanner`) are reachable, as they are for `github-vuln-watcher` today.
- A `Schedule` CR with `trigger.phases: [execution]` produces exactly one Job per night; that is the executor's documented behaviour, not something this change implements.

## Acceptance Criteria

- [ ] **The task type is registered without disturbing the existing four** — a factory spec asserts `provider.Get(ctx, "github-fleet-scan")` returns an agent and no error, and that `github-update-go`, `build-fix`, `healthcheck` and `oauth-probe` still resolve. Evidence: the spec's assertions and its exit code.
- [ ] **The two populations are gated exactly as the retired watchers gate them** — table-driven specs over a fake repo list and fake maintainer configs assert: the vuln population is allowlist ∩ `goUpdate.autoUpdate` consent ∩ `go.mod` present; the stale population is allowlist ∩ `go.mod` present **and parsable** ∩ consent. Each spec names the repo it expects to be excluded and the gate that excluded it. Evidence: the per-row assertions.
- [ ] **The stale rule is withdrawn-only** — with a fake classifier returning `stale` for each of the three reasons in turn, only the `withdrawn` verdict becomes a finding; the package-absent and version-unaffected verdicts do not, and the run files no task for them. Evidence: the asserted finding sets.
- [ ] **Identifiers match the frozen watcher namespaces** — golden specs assert these exact values, which were derived from the retired watchers' own sources:
  - `DeriveScanVulnTaskID("bborbe", "ip", ["GO-2026-0001", "CVE-2026-9999"])` → `5b8f26d2-bae1-538d-b245-f0a81db06c23`
  - `DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, ["GO-2026-0002"])` → `064c07cc-2d45-5b18-af93-c4c9b2741f40`
  - reordering the vuln ID list (`["CVE-2026-9999", "GO-2026-0001"]`) yields the same vuln UUID; the same input twice yields the same UUID in both families; generation `2` yields a different stale UUID than generation `1` (the frozen seed contract, not a path the nightly run exercises).
  Evidence: `go test ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='ScanTaskID'` exits 0 and its output names the two UUIDs above; a run whose output omits them fails the criterion.
- [ ] **A repo needing both families files two distinct tasks, and the frontmatter is the watchers' envelope** — for a candidate carrying both a vuln set and a stale set, the scan builds two `CreateCommand`s whose `task_type`, `assignee`, `phase`, `status`, `stage`, `repo`, `clone_url` and `ref` are identical, whose `task_identifier`s differ, and whose vuln command carries `vulns`/`vuln_count` while the stale command does not. Evidence: the two asserted frontmatter maps.
- [ ] **A second run over an unchanged fleet files nothing** — driving the step twice with the same fake GitHub/OSV inputs, the second run returns zero commands and logs `scanned=N, already_recorded=M` where `N` and `M` equal the first run's `scanned` and `filed`. Evidence: the two recorded log lines and the zero-length second command list.
- [ ] **The scan makes no model call** — the phase the task type composes contains no Claude step; a spec asserts the composed step list. Evidence: the asserted step list and the absence of a runner in the construction.
- [ ] **`make precommit` exits 0** on the changed tree. Evidence: exit code and the `ready to commit` line.

## Verification

### Container-executable (runs inside the YOLO container at prompt time)

- `ROOTDIR=/workspace make precommit` — ensure, format, generate, test, check and addlicense all pass.

### Operator-executable (runs on the host after PR merge)

- The nightly Job and the dev proof belong to the `bborbe/nuke` manifest change and the vault task, not to this spec.

## Desired Behavior

A Job whose task carries `task_type: github-fleet-scan` runs one deterministic step:

1. **List the population.** Fetch `REPO_ALLOWLIST` with the retired watcher's own semantics and its live value: `github.com/bborbe/*,!github.com/bborbe/go-skeleton` — a comma-separated list where `*` is a glob and a `!` prefix excludes. `OWNER=bborbe` supplies the owner scope; an empty allowlist means allow-all within that owner. Reuse `github.com/bborbe/maintainer/repoallowlist` rather than writing a parser. Never expect the repo list in the task body — the task is the recurring template, not data.
2. **Gate each repo.** Read `go.mod` presence/parsability and the `.maintainer.yaml` `goUpdate.autoUpdate` consent, then form the two populations above.
3. **Scan each repo.** Clone it, run its gate targets, and extract the firing advisory IDs. Classify each suppressed advisory on the four ignore surfaces (`Makefile`, `Makefile.precommit`, `.trivyignore`, `.osv-scanner.toml`) and keep only the `withdrawn` verdicts.
4. **File tasks.** Build one `CreateCommand` per repo needing a change and publish it through the same path the watchers used. The identifier comes from the frozen namespace, so the controller's dedup absorbs a re-run.
5. **Report.** Log `scanned=<repos scanned>`, `filed=<commands published>`, and on a second run `already_recorded=<identifiers already present>`.
6. **Exit.** No polling, no cursor, no PVC. A missed run is visible as a missing Job, not as a stalled service.

## Constraints

- One binary, one image: the scan is a new task type in `pkg/factory`'s provider map, not a new repo or image.
- Reuse `OSVFetcher`, `IgnoreClassifier`, `parseVulnsList` and the existing GitHub client. Do not re-port them.
- The frozen namespaces are copied verbatim: `5c3bcb6b-fb0f-4c61-a4c3-8a17fd037f52` (vuln-drift) and `4f2a7c1e-9b3d-4c58-8e21-6d0b7a5c9f13` (stale-ignore). Never mint a new one.
- The two seed formats are copied verbatim from the retired watchers and must not drift:
  - vuln: `vuln-drift-<owner>-<repo>-<comma-joined deduped sorted ids>`
  - stale: `stale-ignore-<owner>-<repo>-g<generation>-<comma-joined sorted ids>` (sorted, **not** deduped — deduping would change the identifier of an already-filed item)
- No `time.Now()`: inject `libtime.CurrentDateTimeGetter`.
- Check `ctx.Done()` inside the per-repo loop.
- Read and write task sections through `MarshalSectionTyped` / `ExtractSection`, never `strings.Index`.
- Guard `result == nil`.
- On failure emit `Status: failed` or `needs_input`; never `NextPhase: human_review`, never mutate `assignee` or `status`.
- The live `github-update-go`, `build-fix` and `healthcheck` behaviour must stay byte-identical — the scan gets its own Config CR on the same image.

## Failure Modes

| Trigger | Expected behavior | Detection | Recovery |
|---|---|---|---|
| GitHub rate limit mid-scan | run ends `Status: failed`, naming the repo it stopped at and how many were scanned; no task is filed for an unscanned repo | the failure message carries both the repo and the `scanned=` count | the next night's Job re-runs the full population; the run's own `scanned=` line confirms it reached the same `N` |
| OSV transport error for one advisory | that ignore is `unresolved`; it is never treated as stale and never removed | the classification is recorded with its `unresolved` reason | none needed — the next run re-queries |
| Clone or gate failure for one repo | that repo is skipped and counted; the run continues | `scanned=` is lower than the population the allowlist yields | fix the repo's gate; the next run picks it up |
| Publish failure | run fails and reports how many commands were published before the failure; a partial publish is never reported as success | the failure message carries `filed=` up to the failure | re-run the Job; the frozen identifiers make the already-published ones no-ops |
| Empty population | `scanned=0` is logged and the run completes; it is not a silent no-op | `scanned=0` in the run log | check `REPO_ALLOWLIST`/`OWNER` — an empty population is a config fault, not a clean fleet |
| Job runs twice in one night, or at 23:59 vs 00:01 | the second run is a no-op over an unchanged fleet; the window is the scheduler's, not the code's — the scan never reads wall-clock to decide whether to run | `already_recorded=M` equal to the first run's `filed` | none — idempotency absorbs the double run |

## Security / Abuse Cases

- The repo population is bounded by `REPO_ALLOWLIST`; no caller-supplied repo reaches a clone.
- Clone destinations are derived from the task identifier, as the update lane already does; no caller-controlled path.
- The GitHub token is the lane's existing App token, never logged.
- The scan writes only task records; it never pushes, merges or edits a repo.

## Suggested Decomposition

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Register `github-fleet-scan` and implement the population: allowlist, `go.mod` presence/parsability, consent, clone + gate scan, `scanned`/`filed` logging | 1–3, 6 | 1, 2, 7 | — |
| 2 | Withdrawn-only stale classification and the stale task body | 3, 4 | 3 | 1 |
| 3 | Task building, publishing and idempotency, with the frozen identifiers and the `already_recorded` log | 4, 5 | 4, 5, 6 | 1, 2 |

## Do-Nothing Option

Both watchers keep running as they do: `github-vuln-watcher` holds 2 CPU / 8Gi and a 40Gi datadir between 12-hourly scans, and the stale-ignore pass keeps riding a 10-minute poll loop with no fixed time and no existence without the service. The parent goal's resource criterion stays blocked, and the nightly-scan task stays open.

**Rejected alternative — run `github-vuln-watcher` as a `CronJob` and scale the StatefulSet to zero between runs.** It frees the same requests without touching this binary, but it does not absorb the stale-ignore pass (which has no schedule of its own and would still need a second mechanism), and it leaves two scanners with two clocks and two task-identifier families. Rejected in favour of one run that produces both task families from one code path — the decision is recorded in the vault task `Run the Fleet Vuln and Stale-Ignore Scan as a Nightly Agent Job`, which superseded the CronJob task on 2026-11-25.
