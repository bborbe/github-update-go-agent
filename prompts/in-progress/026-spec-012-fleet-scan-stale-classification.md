---
status: approved
spec: [012-nightly-fleet-scan]
created: "2026-11-26T08:42:49Z"
queued: "2026-11-26T12:50:35Z"
---

# Fleet scan: withdrawn-only stale-ignore classification

<summary>
- A suppressed advisory is only ever a finding when its upstream record says it was withdrawn.
- An advisory whose package left the module graph stays suppressed, and gets no task.
- An advisory whose in-use version is no longer affected stays suppressed, and gets no task.
- A verdict that could not be computed stays suppressed, and gets no task.
- The scan never removes a suppression itself; it only reports the ones it can prove dead.
- A repository with no suppressions never queries the vulnerability database.
- The scan records which withdrawn advisories it found, per repository, on the run report.
- The operator-readable text filed with a withdrawn-advisory task names each advisory.
- The stale half reuses the classifier the update lane already ships instead of a second implementation.
- Repositories outside the stale population are untouched by this pass.
</summary>

<objective>
Add the stale-ignore family to the nightly scan: for every repository in the stale population, classify its suppressed advisory IDs with the existing classifier and keep only the verdicts that say the advisory was withdrawn. Everything else — package absent, version unaffected, legitimate, unresolved — is not a finding.
</objective>

<context>
Prompt 1 of this spec (`1-spec-012-fleet-scan-register-and-population.md`) must have landed before this prompt runs: it creates `pkg/fleet_source.go`, `pkg/fleet_scan_population.go` (`FleetScanConfig`, `ScanCandidateFacts`, `selectPopulations`), `pkg/steps_fleet_scan.go` (`FleetScanDeps`, `NewFleetScanStep`, `ScanRepoResult`, `FleetScanReport`, the per-repo clone/gate/scan loop), and the registered `github-fleet-scan` task type. If any of those is absent, stop — this prompt depends on prompt 1. Read all of them before changing anything.

Read these files before writing anything:
- `pkg/steps_fleet_scan.go` — the per-repo loop you extend. `loadSuppressedVulnIDs` is already called there to filter the vuln findings; reuse that same call for classification.
- `pkg/osv_classifier.go` — the `IgnoreClassifier` interface (`Classify(ctx, workdir string, ids []string) []IgnoreClassification`), `NewOSVIgnoreClassifier(fetcher OSVFetcher)`.
- `pkg/osv_types.go` — `IgnoreClassification{ID, Outcome, Reason, Package, InUseVersion}`, `IgnoreOutcomeStale`, `IgnoreOutcomeLegitimate`, `IgnoreOutcomeUnresolved`, `IgnoreReasonWithdrawn`, `IgnoreReasonPackageAbsent`, `IgnoreReasonVersionNotAffected`, `IgnoreReasonVersionAffected`, `IgnoreReasonNotGo`, `IgnoreReasonNotFound`, `IgnoreReasonTransportError`, `IgnoreReasonBudgetExceeded`, `IgnoreReasonUnresolvableVersion`.
- `pkg/osv_fetcher.go` — `NewOSVHTTPFetcher()`, the production network path the factory must wire.
- `pkg/scanner_table.go` — `loadSuppressedVulnIDs(ctx, workdir) (map[string]bool, error)`.
- `pkg/stale_ignore_removal.go` — `staleClassifications` and the `stale_ignore_removal_test.go` table style. This is the file that performs removal during an update PR; the scan must NOT call `removeStaleIgnores`, and must not change that file.
- `pkg/factory/factory.go` — how `CreateAgent` wires `updatepkg.NewOSVIgnoreClassifier(updatepkg.NewOSVHTTPFetcher())` into `NewPlanningStep`; mirror that construction for the scan.
- `pkg/export_test.go` — the external-test export block.
- `pkg/osv_classifier_test.go` — the `mocks.IgnoreClassifier` stubbing style (`ClassifyReturns`, `ClassifyCallCount`).
- `main.go` — `buildFleetScanDeps`; `cmd/run-task/main.go` — its counterpart.

Library facts (already verified; use exactly these):
- `IgnoreClassifier.Classify` never returns an error: every transport, status, parse or graph failure becomes an `unresolved` verdict.
- The three stale reasons the classifier can emit are `IgnoreReasonWithdrawn`, `IgnoreReasonPackageAbsent`, `IgnoreReasonVersionNotAffected`. Only `IgnoreReasonWithdrawn` is a finding for the scan.
- `github.com/bborbe/github-update-go-agent/pkg/maintainerconfig` is the alias package already imported by the scan step.

Read the coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-licensing-guide.md`

Read the approved spec `specs/in-progress/012-nightly-fleet-scan.md`: Summary bullet 4, Desired Behavior 3, Constraints (the frozen namespaces and seeds are the next prompt's concern, not this one), Failure Modes row 2, and Acceptance Criterion 3.

This run uses `workflow: worktree`, so the execution container masks the repo's `.git`. This prompt needs no git commands. Run `ROOTDIR=/workspace make precommit` for verification.
</context>

<requirements>
1. Create `pkg/fleet_scan_stale.go`:

```go
// withdrawnOnly returns the IDs whose classification says the advisory was
// withdrawn, sorted. Every other outcome — legitimate, unresolved — and every
// other stale reason — package absent, version not affected — is dropped: the
// scan files a removal task only for an advisory upstream has withdrawn.
func withdrawnOnly(classifications []IgnoreClassification) []string

// renderStaleBody renders the operator-readable body of a stale-ignore task.
// It names the count, lists every advisory ID, and states that the scan removed
// nothing — the update lane's in-PR classifier decides the removal. Keep it
// under a few hundred bytes; the controller caps a body at 500 KiB.
func renderStaleBody(ids []string) string
```

`withdrawnOnly` keeps exactly `c.Outcome == IgnoreOutcomeStale && c.Reason == IgnoreReasonWithdrawn`, returns the `ID`s sorted with `sort.Strings`, and returns nil for an empty input. It must not look at `Package` or `InUseVersion`.

2. Extend `ScanRepoResult` in `pkg/steps_fleet_scan.go` with the stale family:

```go
	StaleIDs []string `json:"stale_ids,omitempty"`
```

3. Extend the per-repo scan loop in `Run`. The loop already computes `suppressed` (the `map[string]bool` from `loadSuppressedVulnIDs`) for the vuln filter. After the vuln IDs are computed, and only when the repo is in the stale population:
- `ids` = the keys of `suppressed`, sorted with `sort.Strings`. When `len(ids) == 0`, skip classification entirely — do not call `Classify` (no suppressions means nothing to classify, and no network call).
- `verdicts := deps.Classifier.Classify(ctx, workdir, ids)`.
- `StaleIDs = withdrawnOnly(verdicts)`.
The classification must run inside the same workdir lifetime as the clone (before the workdir is removed) and must respect `ctx`: the classifier already bounds itself, but re-check `ctx.Done()` before the call and treat a cancelled context as the run's cancellation (return the wrapped error, as the loop already does).

Track stale-population membership with a set built from `stalePop` (the second return of `selectPopulations`) — a repo in both populations is cloned once and gets both families in one pass.

4. Add the classifier to the dependency struct in `pkg/steps_fleet_scan.go`:

```go
	Classifier      IgnoreClassifier
```

Wire it in both entry points:
- `main.go` `buildFleetScanDeps`: `updatepkg.NewOSVIgnoreClassifier(updatepkg.NewOSVHTTPFetcher())` — the same construction `CreateAgent` uses for the planning step.
- `cmd/run-task/main.go`: the same construction with the local token.
- `pkg/factory/factory_test.go`: the zero-value `updatepkg.FleetScanDeps{}` argument already compiles; a nil classifier must not be dereferenced when the population is empty. Guard the call with the `len(ids) == 0` check from requirement 3, and treat a nil `Classifier` as "no stale classification" (log at `glog.V(2)` and leave `StaleIDs` nil) so a local replay without the seam still runs.

5. Report: the `## Fleet Scan` section already carries `Repos []ScanRepoResult`; the new `StaleIDs` field flows through automatically because the section is marshalled with `agentlib.MarshalSectionTyped`. Do not add a second section and do not change the heading.

6. Tests in `pkg/fleet_scan_stale_test.go`, `package pkg_test`, outer `Describe("FleetScanStale", ...)`:
- A `DescribeTable` over `IgnoreClassification{Outcome, Reason}` → expected kept IDs, with these rows:
  1. `stale` / `withdrawn` → the ID is kept.
  2. `stale` / `package-absent` → dropped.
  3. `stale` / `version-not-affected` → dropped.
  4. `legitimate` / `version-affected` → dropped.
  5. `unresolved` / `transport-error` → dropped.
  6. `unresolved` / `not-found` → dropped.
  7. an empty input → nil.
  8. a mixed input of all six verdicts for one ID each → only the withdrawn ID survives, and the result is sorted.
- `renderStaleBody`: the body contains every ID verbatim and the count; the body contains no `/`-only path assumptions and stays under 2000 bytes for 20 IDs.
- Step-level, in `pkg/steps_fleet_scan_test.go` (extend the existing `Describe("FleetScanStep", ...)`): with `mocks.IgnoreClassifier` returning a mixed verdict set, a repo in the stale population produces `StaleIDs` containing only the withdrawn ID, and a repo outside the stale population produces none and never calls `Classify` (`ClassifyCallCount()` is 0).
- Step-level serialization contract: read the written `## Fleet Scan` section back with `agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")` (or assert the raw markdown contains `"stale_ids"`), and assert a stale-population repo's report carries its withdrawn IDs under `stale_ids` while a repo with no stale findings omits the key entirely (`omitempty`). A struct-field assertion does not prove the tagged key reaches the persisted report.
- Step-level: a stale-population repo with no suppression surfaces yields `ClassifyCallCount() == 0` and `StaleIDs` nil.
- Fixture the workdir through `mocks.GitOps.CloneAtRefStub`: `os.MkdirAll(workdir, 0o755)` and write the fixture `Makefile`, `go.mod` and `.trivyignore` (or `.osv-scanner.toml`) into it, as `pkg/steps_planning_test.go` and `pkg/stale_ignore_removal_test.go` do — `loadSuppressedVulnIDs` and the classifier both read the workdir from disk, so a clone stub that creates no files yields no suppressions and no classification.

7. Export `WithdrawnOnly = withdrawnOnly` and `RenderStaleBody = renderStaleBody` in `pkg/export_test.go` (the tests live in `pkg_test`). Regenerate nothing — no new counterfeiter directive is introduced by this prompt.

Every new `.go` file carries the project's BSD license header (copy it from `pkg/osv_classifier.go`).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- The stale rule is withdrawn-only. The three-condition verdict the in-PR classifier implements (withdrawn, package absent, version unaffected) is deliberately NOT the removal trigger here — do not "improve" the filter to include the other reasons.
- Do NOT change `pkg/osv_classifier.go`, `pkg/osv_types.go`, `pkg/osv_fetcher.go` or `pkg/stale_ignore_removal.go`. The scan consumes verdicts; it does not change what the update PR removes.
- The scan writes only task records; it never removes a suppression from a repository.
- No `time.Now()`: any clock read goes through the injected `libtime.CurrentDateTimeGetter`.
- Check `ctx.Done()` inside the per-repo loop.
- Read and write task sections through `MarshalSectionTyped` / `ExtractSection`, never `strings.Index`.
- Guard `result == nil`.
- On failure emit `Status: failed` or `needs_input`; never `NextPhase: human_review`, never mutate `assignee` or `status`.
- The live `github-update-go`, `build-fix` and `healthcheck` behaviour must stay byte-identical.
- Errors go through `github.com/bborbe/errors` with `ctx`, never `fmt.Errorf`.
- No new dependency.
- No new scenario: the filter and the step are fully covered by unit tests with counterfeiter fakes.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must exit 0.

The stale suite passes and actually ran:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='FleetScanStale' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-stale.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<8){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-stale.log
```

The full fleet-scan family still passes:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='FleetScan' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-all.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<15){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-all.log
```

Confirm the rule is withdrawn-only and the existing classifier is untouched:

```bash
grep -c 'IgnoreReasonWithdrawn' pkg/fleet_scan_stale.go           # expect: >= 1
! grep -q 'IgnoreReasonPackageAbsent' pkg/fleet_scan_stale.go
! grep -q 'IgnoreReasonVersionNotAffected' pkg/fleet_scan_stale.go
grep -c 'Classifier' pkg/steps_fleet_scan.go                      # expect: >= 2
grep -c 'NewOSVIgnoreClassifier' main.go                          # expect: 1
grep -c 'NewOSVIgnoreClassifier' cmd/run-task/main.go             # expect: 1
! grep -q 'removeStaleIgnores' pkg/fleet_scan_stale.go
```

Self-check before finishing: walk every `<summary>` bullet and every requirement against the diff; re-run the focused commands and `make precommit`.
</verification>

<!--
Open questions carried from the spec (the spec is silent; the choices below are
deliberate and must not be silently reversed):

1. The spec's Summary bullet 4 and Constraint forbid reusing the in-PR
   three-condition verdict. `withdrawnOnly` therefore keeps ONLY
   `stale`/`withdrawn`. Do not widen it to package-absent or version-unaffected
   even though the classifier computes them.

2. Classification runs only for repos in the stale population, on the suppressed
   IDs already loaded for the vuln filter. A repo with zero suppressions never
   calls the classifier and never touches OSV — the spec's resource goal depends
   on this.

3. A nil Classifier (local replay) is treated as "no stale classification" rather
   than an error, so a local run still exercises the vuln half.
-->
