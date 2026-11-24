---
status: completed
spec: [010-osv-stale-ignore-classification]
summary: Added the OSV stale-ignore classifier library (injectable fetcher, go.mod graph with replace/exclude, version-range math, Go-index alias resolution, execution re-check) with 23 focused specs; make precommit exits 0 (run with the harness's GIT_AUTHOR_* identity vars unset, since they otherwise override an unrelated repo bot-identity test).
execution_id: 8da7ef7e-6d30-5470-a816-9db48d8d4090-exec-024-osv-stale-ignore-classifier
dark-factory-version: dev
created: "2026-11-24T08:10:00Z"
queued: "2026-11-24T08:06:36Z"
started: "2026-11-24T08:06:37Z"
completed: "2026-11-24T08:30:48Z"
branch: dark-factory/osv-stale-ignore-classification
---

# OSV stale-ignore classifier

<summary>
- Adds a self-contained Go library that decides, for one operator-approved vulnerability ignore, whether the ignore is provably dead or still describes a real risk.
- Every ignored advisory is classified into exactly one of three outcomes: stale, legitimate, or unresolved.
- An ignore is stale when the advisory was withdrawn, when its Go package is not in the repo's module graph, or when the repo's in-use version sits outside every affected range.
- An advisory that OSV merged into a different record is found through the Go vulnerability index instead of being written off as "not found".
- The repository's own module file is the source of truth for package presence and in-use version, including modules redirected or removed by replace/exclude.
- A network or parse problem never deletes anything: it is recorded as unresolved and the caller keeps the ignore.
- Network access sits behind an injectable seam, so tests replay recorded responses and never touch the network.
- Requests are bounded per call and the whole classification is bounded by a single budget.
- No new dependency is added; the already-required Go module tooling supplies parsing and version comparison.
</summary>

<objective>
Build the deterministic classification contract the planning and execution phases will consume: a Go library in package `pkg` that turns the set of suppressed advisory IDs plus a checked-out workdir into a list of `(id, outcome, reason)` verdicts against OSV and the repo's `go.mod`. This is the foundation the rest of the feature stands on — planning records the verdicts and execution removes only the stale ones.
</objective>

<context>
Read `CLAUDE.md` (if present) for project conventions.

Read these files before writing anything:
- `pkg/scanner_table.go` — `loadSuppressedVulnIDs`, `suppressedIDRegexp`, and the three per-surface loaders. The classifier consumes the same IDs; do NOT change these functions or how findings are filtered.
- `pkg/maintainerconfig/fetcher.go` — the fetcher seam this library must mirror: an interface with a doc comment, a counterfeiter directive, a production constructor, an unexported base-URL constructor for tests, a 404 sentinel (`ErrFileNotFound`), `net/http` with a hard `Timeout`, `io.ReadAll(http.MaxBytesReader(...))`, and `github.com/bborbe/errors` wrapping with `ctx`. Note its body-redaction rationale for non-2xx responses.
- `pkg/maintainerconfig/export_test.go` — how the base-URL test constructor is exposed only to tests.
- `pkg/export_test.go` — the existing test-export block; add the new exports there.
- `pkg/module_versions.go` — `ModuleVersion`, `parseModuleList`, `moduleForPackage`, and the `//counterfeiter:generate` directive style.
- `pkg/steps_planning.go` — `normalizeVersion` (reuse it for version normalization; it handles `go1.27.4` → `v1.27.4` and passes `v`-prefixed module versions through).
- `pkg/pkg_suite_test.go` — the Ginkgo suite is `TestSuite`; the tests live in `package pkg_test`.

Read the coding guides:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` (Ginkgo/Gomega patterns, `DescribeTable`).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` (counterfeiter directives and generated mocks).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` (`github.com/bborbe/errors` with `ctx`, never `fmt.Errorf`).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md` (per-item budget checks).

Read the approved spec `specs/in-progress/010-osv-stale-ignore-classification.md`: Desired Behavior 1-9, Constraints, Failure Modes rows 1-3, and Acceptance Criterion 1.

This run uses `workflow: worktree`, so the execution container masks the repo's `.git`. This prompt needs no git commands. Run `ROOTDIR=/workspace make precommit` for verification.
</context>

<requirements>
1. Create `pkg/osv_types.go` with the wire types and the verdict contract. Use exactly these shapes (the JSON tags are the OSV schema keys):

```go
// OSVVulnerability is the subset of an OSV advisory record the classifier reads.
type OSVVulnerability struct {
	ID        string        `json:"id"`
	Aliases   []string      `json:"aliases,omitempty"`
	Withdrawn string        `json:"withdrawn,omitempty"`
	Affected  []OSVAffected `json:"affected,omitempty"`
}

type OSVAffected struct {
	Package OSVPackage `json:"package"`
	Ranges  []OSVRange `json:"ranges,omitempty"`
}

type OSVPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

type OSVRange struct {
	Type   string     `json:"type"`
	Events []OSVEvent `json:"events,omitempty"`
}

type OSVEvent struct {
	Introduced   string `json:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty"`
}

// GoVulnIndex is https://vuln.go.dev/index/vulns.json: module path -> the
// advisories that affect it.
type GoVulnIndex map[string][]GoVulnIndexEntry

type GoVulnIndexEntry struct {
	ID       string `json:"id"`
	Modified string `json:"modified,omitempty"`
}

// IgnoreClassification is the verdict for one suppressed advisory ID.
type IgnoreClassification struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	// Package is the module path the advisory names (or "stdlib"), recorded so
	// execution can re-check local conditions without re-fetching OSV.
	Package string `json:"package,omitempty"`
	// InUseVersion is the version planning classified against, recorded so
	// execution can tell whether the module graph moved.
	InUseVersion string `json:"in_use_version,omitempty"`
}
```

2. Add the outcome and reason constants in `pkg/osv_types.go`:

```go
const (
	IgnoreOutcomeStale      = "stale"
	IgnoreOutcomeLegitimate = "legitimate"
	IgnoreOutcomeUnresolved = "unresolved"
)

const (
	IgnoreReasonWithdrawn           = "withdrawn"
	IgnoreReasonPackageAbsent       = "package-absent"
	IgnoreReasonVersionNotAffected  = "version-not-affected"
	IgnoreReasonVersionAffected     = "version-affected"
	IgnoreReasonNotGo               = "not-go"
	IgnoreReasonNotFound            = "not-found"
	IgnoreReasonTransportError      = "transport-error"
	IgnoreReasonBudgetExceeded      = "budget-exceeded"
	IgnoreReasonUnresolvableVersion = "unresolvable-version"
)
```

3. Create `pkg/osv_fetcher.go` with the network seam, modelled on `pkg/maintainerconfig/fetcher.go`:

```go
//counterfeiter:generate -o ../mocks/osv_fetcher.go --fake-name OSVFetcher . OSVFetcher

// OSVFetcher fetches one advisory record by ID and the Go vulnerability index.
// Implementations MUST be safe for concurrent use.
type OSVFetcher interface {
	// FetchVuln returns the advisory record, or ErrAdvisoryNotFound on HTTP 404.
	FetchVuln(ctx context.Context, id string) (*OSVVulnerability, error)
	// FetchGoIndex returns the parsed Go vulnerability index.
	FetchGoIndex(ctx context.Context) (*GoVulnIndex, error)
}

// ErrAdvisoryNotFound is returned by FetchVuln on HTTP 404.
var ErrAdvisoryNotFound = stderrors.New("osv: advisory not found")

func NewOSVHTTPFetcher() OSVFetcher
func newOSVHTTPFetcherWithBase(vulnBaseURL, indexURL string) OSVFetcher
```

Behaviour:
- `NewOSVHTTPFetcher()` wires `vulnBaseURL = "https://api.osv.dev/v1/vulns"` and `indexURL = "https://vuln.go.dev/index/vulns.json"`.
- The internal `http.Client` uses a hard `Timeout: 10 * time.Second` (define `const osvRequestTimeout = 10 * time.Second`).
- `FetchVuln` GETs `vulnBaseURL + "/" + url.PathEscape(id)` via `http.NewRequestWithContext`. 404 returns `ErrAdvisoryNotFound`. Any other non-2xx returns a wrapped error naming the status code and a body fingerprint, exactly like `checkStatus` in the maintainerconfig fetcher (do NOT include the raw body). A decode failure is a wrapped error.
- `FetchGoIndex` GETs `indexURL`, reads the body through `http.MaxBytesReader` capped at `64 << 20` (64 MiB), and JSON-decodes into `*GoVulnIndex`. Non-2xx and decode failures are wrapped errors.
- Log a `glog.V(2)` line per request (method, URL path, status, bytes), following the fetcher's logging style. Never log the raw body.

4. Create `pkg/osv_classifier.go` with the classifier contract:

```go
//counterfeiter:generate -o ../mocks/ignore_classifier.go --fake-name IgnoreClassifier . IgnoreClassifier

// IgnoreClassifier classifies suppressed advisory IDs against OSV and a
// checked-out repository's module graph. It never returns an error: any
// transport, status, parse or graph failure becomes an unresolved verdict so
// planning is never aborted and no suppression is ever removed on a failure.
type IgnoreClassifier interface {
	Classify(ctx context.Context, workdir string, ids []string) []IgnoreClassification
}

func NewOSVIgnoreClassifier(fetcher OSVFetcher) IgnoreClassifier
```

5. Implement `Classify` with these rules, in this order, for each input ID:
   1. Wrap the whole call in `context.WithTimeout(ctx, 60*time.Second)` (`const classifyBudget = 60 * time.Second`). Before each ID, if the budget is done, append `unresolved` with reason `budget-exceeded` for that ID and every remaining one.
   2. Load the module graph once from `<workdir>/go.mod` (see requirement 6). If it cannot be read or parsed, every ID is `unresolved` with reason `transport-error` (a graph failure must never delete anything) — do not abort.
   3. `fetcher.FetchVuln(ctx, id)`:
      - On `errors.Is(err, ErrAdvisoryNotFound)`: resolve the alias through the Go index (requirement 7). If no alias resolves, `unresolved` with reason `not-found`.
      - On any other error: `unresolved` with reason `transport-error`.
   4. On a fetched record, classify it (requirement 8).

6. Module graph (`func loadModuleGraph(workdir string) (versions map[string]string, unresolvable map[string]bool, goDirective string, err error)`), using `golang.org/x/mod/modfile`:
   - `data, err := os.ReadFile(filepath.Join(workdir, "go.mod"))`; `f, err := modfile.Parse("go.mod", data, nil)`.
   - Start `versions[m.Mod.Path] = m.Mod.Version` for every `f.Require` entry (including `// indirect`).
   - For every `f.Replace` entry: when `r.New.Version == ""` (a local-path replace) set `unresolvable[r.Old.Path] = true`; otherwise set `versions[r.Old.Path] = r.New.Version`.
   - For every `f.Exclude` entry: when `versions[e.Mod.Path] == e.Mod.Version`, set `unresolvable[e.Mod.Path] = true`.
   - Return `goDirective = f.Go.Version` (empty when the file has no `go` line). The main module path is not needed for classification.

7. Alias resolution through the Go index (`func resolveAlias(ctx, fetcher, queriedID string, versions map[string]string) *OSVVulnerability`):
   - Fetch the index lazily on the first 404 of a `Classify` call and memoize it for that call only (never package-level state). If the index fetch fails, return nil (the caller records `not-found`; the failure mode requires the ID stay in place).
   - Build the candidate `GO-*` IDs from `index[module]` for every module in `versions`, de-duplicate them, and for each candidate call `FetchVuln` (memoize per-call so a repeated candidate is fetched once). Return the first record whose `Aliases` contains `queriedID` (use `slices.Contains`).
   - Respect the 60-second budget while walking candidates.
   - Reviewer note: this resolves a merged alias by scanning the advisories of the repo's own modules. If the real `index/vulns.json` exposes a direct alias→ID map, use that instead; the recorded-response tests pin whichever mechanism is implemented.

8. Classify a fetched record (`func classifyRecord(record *OSVVulnerability, versions map[string]string, unresolvable map[string]bool, goDirective string) IgnoreClassification`):
   - If `record.Withdrawn != ""` → `stale` / `withdrawn`.
   - Collect the `affected` entries whose `Package.Ecosystem == "Go"`. If there are none → `legitimate` / `not-go` (never remove an advisory we cannot evaluate).
   - If any Go package name is `"stdlib"`: a stdlib advisory is NEVER package-absent. Compare `goDirective` against the stdlib ranges: inside any range → `legitimate` / `version-affected`; outside all ranges → `stale` / `version-not-affected`. Record `Package = "stdlib"` and `InUseVersion = goDirective`.
   - Otherwise, for the module packages:
     - A package is present when its path equals a key in `versions`, or a `versions` key is a path-segment prefix of it.
     - If no package is present → `stale` / `package-absent` (record the first package name).
     - If a present package is in `unresolvable` and no present resolvable package is affected → `unresolved` / `unresolvable-version`.
     - If any present resolvable package's in-use version is inside one of its ranges → `legitimate` / `version-affected` (record that package and version).
     - Otherwise → `stale` / `version-not-affected` (record a present package and its version).

9. Version math (`func versionInRanges(version string, ranges []OSVRange) bool`), using `golang.org/x/mod/semver` and the existing `normalizeVersion`:
   - Only `SEMVER` and `ECOSYSTEM` ranges are evaluated. A range with no evaluable events is skipped.
   - Walk the events in order: `introduced` opens the interval (treat `"0"` as the lowest version); `fixed` closes it as `[introduced, fixed)`; `last_affected` closes it as `[introduced, last_affected]`; a trailing `introduced` with no close is `[introduced, ∞)`.
   - Compare normalized versions with `semver.Compare` (`semver.IsValid` first; an unparseable version makes the range not match).
   - A record whose Go package is present but has no evaluable ranges is `legitimate` (never remove on missing range data).

10. Add `pkg/osv_classifier_recheck.go` (or append to `pkg/osv_classifier.go`) with the execution-time local re-check:

```go
// recheckStale returns the stale classifications that still qualify against
// workdir's go.mod. A withdrawn verdict is taken from the plan. A
// package-absent verdict is dropped when the package is now present. A
// version-not-affected verdict is dropped when the module is now absent, is
// unresolvable, or its in-use version differs from the one planning recorded.
// A go.mod that cannot be read keeps only the withdrawn verdicts (remove
// nothing that depends on local state). It never returns an error.
func recheckStale(ctx context.Context, workdir string, stale []IgnoreClassification) []IgnoreClassification
```

Export it for the external test package by adding `RecheckStale = recheckStale` to `pkg/export_test.go`.

11. Add `NewOSVHTTPFetcherForTest = newOSVHTTPFetcherWithBase` to `pkg/export_test.go`.

12. Tests in `pkg/osv_classifier_test.go` and `pkg/osv_fetcher_test.go`, `package pkg_test`. Use `mocks.OSVFetcher` for the classifier tests (stub `FetchVulnStub` per ID and `FetchGoIndexReturns`). Name the outer `Describe` exactly `Describe("OSV ignore classifier", ...)` so the focus regex `OSV|StaleIgnore` selects it. Cover at least these ten leaf specs, each asserting a concrete `(ID, Outcome, Reason)` triple:
   1. withdrawn record → `stale` / `withdrawn`.
   2. alias-resolved withdrawn record: `FetchVuln` returns `ErrAdvisoryNotFound` for the queried CVE; the index maps a graph module to `GO-2026-4923`; `FetchVuln("GO-2026-4923")` returns a withdrawn record whose `Aliases` contains the CVE → `stale` / `withdrawn`.
   3. package absent from go.mod → `stale` / `package-absent`.
   4. in-use version outside every affected range → `stale` / `version-not-affected`.
   5. in-use version inside an affected range → `legitimate` / `version-affected`.
   6. stdlib advisory is never package-absent: a `stdlib` record is `legitimate` / `version-affected` when the `go` directive is inside the range, and `stale` / `version-not-affected` when outside.
   7. module under a local-path `replace` → `unresolved` / `unresolvable-version`.
   8. require version removed by `exclude` → `unresolved` / `unresolvable-version`.
   9. 404 with no alias → `unresolved` / `not-found`.
   10. transport error → `unresolved` / `transport-error`.
   Build each fixture's `go.mod` in a `t.TempDir()`/`os.MkdirTemp` directory; pass that path as `workdir`. The `DescribeTable` entries count as one spec each, so the focused run reports at least 10.

13. In `pkg/osv_fetcher_test.go`, name the outer `Describe` `Describe("OSV HTTP fetcher", ...)` and use `httptest.NewServer` with `pkg.NewOSVHTTPFetcherForTest(vulnBase, indexURL)` (the maintainerconfig fetcher test is the template). Cover: 200 valid record parses; 404 returns `errors.Is(err, pkg.ErrAdvisoryNotFound)`; 500 returns a wrapped error containing `status 500` and NOT the raw body; index 200 parses into `GoVulnIndex`; a request exceeding the timeout fails.

14. Regenerate the counterfeiter mocks: run `ROOTDIR=/workspace make generate` (it wipes and regenerates `mocks/`). Confirm `mocks/osv_fetcher.go` and `mocks/ignore_classifier.go` exist and are not hand-edited.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- No new dependency. Use `golang.org/x/mod/modfile` and `golang.org/x/mod/semver` (already required) plus the standard library.
- The classifier must be injectable: tests replay recorded responses and never touch the network. The production `OSVFetcher` is the only network path.
- The OSV lookups are `GET https://api.osv.dev/v1/vulns/<id>` and, on 404, `https://vuln.go.dev/index/vulns.json`. Both hosts are already on the pod's egress allowlist.
- Any transport, status, parse or graph failure yields `unresolved`; it never yields `stale` and never aborts the caller. A 404 with no resolvable alias is `unresolved` / `not-found`.
- Per request: 10-second timeout. Whole classification: 60-second budget; an ID past the budget is `unresolved` / `budget-exceeded`.
- Do NOT change `loadSuppressedVulnIDs`, the findings filter, gate-target detection, or scanner-output parsing. This prompt only adds new files plus the two `export_test.go` exports.
- Every new `.go` file carries the project's BSD license header (copy it from `pkg/osv_fetcher.go`'s neighbours such as `pkg/module_versions.go`).
- Errors go through `github.com/bborbe/errors` with `ctx`, never `fmt.Errorf`.
- Never hand-edit generated mocks; regenerate them.
- Do NOT touch `.github/workflows/`.
- No new scenario: the classifier and fetcher are fully covered by unit tests with recorded responses and an in-process HTTP server.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must exit 0.

AC1 — the focused classifier suite runs at least ten specs and passes:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='OSV|StaleIgnore' -ginkgo.v 2>&1 | tee /tmp/osv-focus.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<10){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/osv-focus.log
```

Confirm the seams and generated mocks exist:

```bash
grep -c 'func NewOSVIgnoreClassifier' pkg/osv_classifier.go
# expect: 1

grep -c 'func NewOSVHTTPFetcher' pkg/osv_fetcher.go
# expect: >= 1

grep -c 'func (fake \*OSVFetcher) FetchVuln' mocks/osv_fetcher.go
# expect: 1

grep -c 'func (fake \*IgnoreClassifier) Classify' mocks/ignore_classifier.go
# expect: 1

grep -c 'ErrAdvisoryNotFound' pkg/osv_fetcher.go
# expect: >= 2
```

Confirm the classifier reuses the existing normalizer and the required module tooling:

```bash
grep -c 'golang.org/x/mod/modfile' pkg/osv_classifier.go
# expect: 1

grep -c 'normalizeVersion' pkg/osv_classifier.go
# expect: >= 1
```

Self-check before finishing: walk every `<summary>` bullet and every requirement against the diff; re-run the focused command and `make precommit`.
</verification>
