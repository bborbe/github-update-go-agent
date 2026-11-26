---
status: completed
spec: [012-nightly-fleet-scan]
created: "2026-11-26T08:42:49Z"
queued: "2026-11-26T12:50:35Z"
completed: "2026-11-26T16:10:51Z"
pr-url: https://github.com/bborbe/github-update-go-agent/pull/116
---

# Fleet scan: register the task type, gate the population, scan the vuln family

<summary>
- The agent binary gains a fifth task type that runs one deterministic fleet scan with no model call.
- The scan enumerates the owner's repositories and keeps only those the allowlist permits.
- A repository that never opted in to automatic Go updates is never scanned, and never gets a task.
- A repository without a readable module file is excluded, and one whose module file cannot be parsed is scanned for vulnerabilities but not for stale suppressions.
- Each surviving repository is cloned once, its gate targets are run, and the advisory IDs its scanners report are collected.
- Advisories the operator already suppressed are not treated as findings.
- A repository that cannot be cloned or gated is skipped and the run continues; a rate limit stops the run and names where it stopped.
- The run reports how many repositories it scanned on the task page and in the log.
- The new task type is registered alongside the existing four without changing any of them.
- Every existing task type keeps its current behaviour byte for byte.
</summary>

<objective>
Add the `github-fleet-scan` task type and the first half of the nightly scan: enumerate the allowlisted repositories, gate them on consent and module-file presence, clone the survivors, run their gate targets, and record the firing (unsuppressed) advisory IDs plus a `scanned=` count. This is the foundation the stale-ignore family and the task publishing build on.
</objective>

<context>
Read `docs/design.md` — § 3.3 (task format), § 4.2 (phase/step shape), § 7.2 (auth + capability model). There is no root `CLAUDE.md`.

Read these files before writing anything:
- `pkg/steps_planning.go` — `planningStep`: the canonical clone-and-scan sequence (`ResolveDefaultBranch` → `CloneAtRef` → `detectGateTargets` → `RunTargetFull` → `parseScannerOutput` → `loadSuppressedVulnIDs`), the `ghToken` field convention (`display:"length"`), `setupWorkdir`, `workdirPrefix`, `sanitizePathComponent`, and `NewPlanningStep`'s dependency list.
- `pkg/scanner_table.go` — `detectGateTargets(ctx, workdir) ([]string, error)`, `knownGateTargets`, `parseScannerOutput(target, raw string) []ScannerFinding`, `loadSuppressedVulnIDs(ctx, workdir) (map[string]bool, error)`.
- `pkg/steps_fix_execution.go` — the pure-Go step template: `Name` / `ShouldRun` / `Run`, `md.ReplaceSection`, `agentlib.MarshalSectionTyped`, `failed(...)`.
- `pkg/steps_gh_token.go` — the `needsInput(msg string) *agentlib.Result` and `failed(msg string) *agentlib.Result` helpers this step must reuse.
- `pkg/git/git.go` — the `GitOps` interface (`CloneAtRef`, `ResolveDefaultBranch`).
- `pkg/gate_runner.go` — `GateRunner.RunTargetFull`.
- `pkg/maintainerconfig/fetcher.go` and `pkg/maintainerconfig/export_test.go` — the HTTP-seam pattern to mirror for the two new GitHub seams: doc-commented interface, `//counterfeiter:generate` directive, production constructor, unexported base-URL constructor for tests, a 404 sentinel, `net/http` with a hard `Timeout`, `io.ReadAll(http.MaxBytesReader(...))`, redacted non-2xx bodies (status + body fingerprint, never the body), `github.com/bborbe/errors` wrapping with `ctx`.
- `pkg/factory/factory.go` — `CreateAgentProvider`, the `taskType*` literals, and the named phase-assembly functions (`updaterPhases`, `buildFixPhases`) the composition tests assert against.
- `pkg/factory/factory_test.go` — the `CreateAgentProvider` call site you must update.
- `pkg/factory/export_test.go` — where the factory test exports live.
- `pkg/export_test.go` — the external-test export block.
- `pkg/pkg_suite_test.go` — the Ginkgo suite is `TestSuite`; tests live in `package pkg_test`.
- `main.go` — the `application` struct, `buildRun`, and the `CreateAgentProvider` call.
- `cmd/run-task/main.go` — the second `CreateAgentProvider` call site (local CLI). Both entry points must compile.
- `pkg/export_test.go`'s `RecheckStale` / `RemoveStaleIgnores` entries — the export style.

Library facts (already verified; use exactly these):
- `github.com/bborbe/maintainer/repoallowlist` — `func IsAllowed(allowlist []string, target string) bool`. `target` is `"host/owner/repo"`. An empty/nil allowlist allows everything; a `!`-prefixed entry excludes; excludes always win.
- `github.com/bborbe/github-update-go-agent/pkg/maintainerconfig` re-exports `Config` and `GoUpdateConfig` from `github.com/bborbe/maintainer/maintainerconfig`; the consent flag is `cfg.GoUpdate.AutoUpdate`. `maintainerconfig.Fetcher.Fetch(ctx, owner, repo, ref)` returns `ErrFileNotFound` on 404 and errors on an empty ref.
- `golang.org/x/mod/modfile` — `modfile.Parse(path string, data []byte, fixVersion func(_,_,_ string) (string, error)) (*modfile.File, error)`.

Read the coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-time-injection.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-licensing-guide.md`

Read the approved spec `specs/in-progress/012-nightly-fleet-scan.md`: Desired Behavior 1-3 and 6, Constraints, Failure Modes rows 1, 3, 5, 6, and Acceptance Criteria 1, 2, 7.

This run uses `workflow: worktree`, so the execution container masks the repo's `.git`. This prompt needs no git commands. Run `ROOTDIR=/workspace make precommit` for verification.
</context>

<requirements>
1. Create `pkg/fleet_source.go` with the two GitHub read seams and their production implementations, modelled on `pkg/maintainerconfig/fetcher.go`:

```go
// ErrFleetFileNotFound is returned by FetchFile on HTTP 404.
var ErrFleetFileNotFound = stderrors.New("fleet: file not found at default branch")

// ErrFleetRateLimited is returned by either seam on HTTP 403 or 429, so the
// scan can stop the run instead of treating a rate limit as a per-repo skip.
var ErrFleetRateLimited = stderrors.New("fleet: github rate limited")

//counterfeiter:generate -o ../mocks/repo_lister.go --fake-name RepoLister . RepoLister

// RepoLister enumerates the repositories the GitHub App installation can read.
// Implementations MUST be safe for concurrent use.
type RepoLister interface {
	// ListRepos returns the owner's repositories as "owner/name", sorted, one
	// entry per repo. An empty result is not an error.
	ListRepos(ctx context.Context, owner string) ([]string, error)
}

//counterfeiter:generate -o ../mocks/repo_file_fetcher.go --fake-name RepoFileFetcher . RepoFileFetcher

// RepoFileFetcher reads one small file from a repository's default branch.
// Implementations MUST be safe for concurrent use.
type RepoFileFetcher interface {
	// FetchFile returns the file's bytes at the repo's default branch, or
	// ErrFleetFileNotFound when the path does not exist there.
	FetchFile(ctx context.Context, repo, path string) ([]byte, error)
}

func NewGitHubRepoLister(token string) RepoLister
func NewGitHubRepoFileFetcher(token string) RepoFileFetcher
func newGitHubRepoListerWithBase(token, apiBase string) RepoLister
func newGitHubRepoFileFetcherWithBase(token, apiBase string) RepoFileFetcher
```

Behaviour, for both production constructors:
- `apiBase = "https://api.github.com"`; a single `*http.Client` with `Timeout: 15 * time.Second` (one shared `const fleetRequestTimeout = 15 * time.Second`).
- Headers on every request: `Accept: application/vnd.github+json`, `X-GitHub-Api-Version: 2022-11-28`, and `Authorization: Bearer <token>` only when the token is non-empty.
- `ListRepos` GETs `/installation/repositories?per_page=100&page=<n>`, starting at page 1, and follows pages until a page returns fewer than 100 repositories. Decode `{"repositories":[{"full_name":"owner/name"}]}`. Sort the result with `sort.Strings`. HTTP 403/429 returns an error wrapping `ErrFleetRateLimited`; any other non-2xx returns a wrapped error naming the status and a body fingerprint (copy `checkStatus`'s redaction: status code, `body_sha256_prefix`, `body_bytes` — never the body).
- `FetchFile` GETs `/repos/<owner>/<repo>/contents/<path>` with no `ref` query parameter (omitting it reads the default branch). `repo` is split on `/`; a malformed `repo` returns a wrapped error. HTTP 404 returns `ErrFleetFileNotFound`; 403/429 returns an error wrapping `ErrFleetRateLimited`; other non-2xx is a wrapped error with the redacted fingerprint. Decode the `{"encoding","content"}` response: `encoding` must be `base64`, the content is base64-decoded with `base64.StdEncoding` (GitHub wraps lines; strip `\n` before decoding). Body cap `1 << 20` via `http.MaxBytesReader`.
- Log one `glog.V(2)` line per request (method, URL path, status, bytes). Never log the token or the body.

2. Create `pkg/fleet_scan_population.go` with the scan scope and the population gate:

```go
// FleetScanConfig is the deployment-supplied scan scope.
type FleetScanConfig struct {
	Owner     string   // OWNER — the owner whose repositories the lister enumerates
	Allowlist []string // REPO_ALLOWLIST — host/owner/repo entries; '*' globs, '!' excludes
	Stage     string   // STAGE — stamped on every task the scan files
}

// ParseRepoAllowlist splits a comma-separated REPO_ALLOWLIST value into
// trimmed, non-empty entries. An empty or all-blank value returns nil, which
// repoallowlist reads as allow-all within the owner.
func ParseRepoAllowlist(raw string) []string

// ScanCandidateFacts is one allowlist-visible repository and its gate facts.
type ScanCandidateFacts struct {
	Repo          string // "owner/name"
	GoModPresent  bool
	GoModParsable bool
	Consent       bool
}

// selectPopulations partitions facts into the two scan populations.
//
// vuln  = allowlist ∩ GoModPresent ∩ Consent
// stale = allowlist ∩ GoModPresent ∩ GoModParsable ∩ Consent
//
// Allowlist membership is decided with repoallowlist.IsAllowed over
// scanTarget(facts.Repo). Both slices preserve the input order.
func selectPopulations(allowlist []string, facts []ScanCandidateFacts) (vuln, stale []ScanCandidateFacts)

// scanTarget renders the allowlist target form for a repo: "github.com/<repo>".
func scanTarget(repo string) string
```

`const fleetHost = "github.com"` feeds `scanTarget`. Do not write an allowlist matcher — call `repoallowlist.IsAllowed`.

3. Create `pkg/steps_fleet_scan.go` with the step and its dependency struct:

```go
// FleetScanDeps is the injected surface of the nightly fleet scan.
type FleetScanDeps struct {
	Config          FleetScanConfig
	GhToken         string `display:"length"`
	Lister          RepoLister
	Files           RepoFileFetcher
	Consent         maintainerconfig.Fetcher
	GitOps          git.GitOps
	Gate            GateRunner
	CurrentDateTime libtime.CurrentDateTimeGetter
}

func NewFleetScanStep(deps FleetScanDeps) agentlib.Step

// ScanRepoResult is the per-repository outcome of one successful scan.
type ScanRepoResult struct {
	Repo     string   `json:"repo"`
	Ref      string   `json:"ref"`
	CloneURL string   `json:"clone_url"`
	VulnIDs  []string `json:"vuln_ids,omitempty"`
}

// FleetScanReport is the ## Fleet Scan section payload on the scan task.
type FleetScanReport struct {
	ScannedAt string           `json:"scanned_at"`
	Scanned   int              `json:"scanned"`
	Repos     []ScanRepoResult `json:"repos,omitempty"`
}
```

`Name()` returns exactly `"fleet-scan"`. `ShouldRun` always returns `true` — there is no replay guard: a re-triggered Job re-scans, and the identifier dedup in the publishing half absorbs the duplicates (spec Failure Modes row 6: the scan never reads wall-clock to decide whether to run).

`Run` executes, in order:
1. If `deps.Config.Owner` is empty → `needsInput("fleet-scan: OWNER is empty — config fault, not a clean fleet")`. Never `NextPhase: human_review`.
2. `repos, err := deps.Lister.ListRepos(ctx, deps.Config.Owner)`; on error return `failed(...)` naming the error (the run cannot proceed without a population).
3. Build `[]ScanCandidateFacts` for allowlisted repos only. For each repo, in the lister's order:
   - Skip immediately (no API call, not counted) when `!repoallowlist.IsAllowed(deps.Config.Allowlist, scanTarget(repo))`.
   - `goMod, err := deps.Files.FetchFile(ctx, repo, "go.mod")`:
     - `errors.Is(err, ErrFleetFileNotFound)` → `GoModPresent = false`; continue to the next repo.
     - `errors.Is(err, ErrFleetRateLimited)` → return `failed("fleet-scan: github rate limited at repo=<repo> scanned=<n>")` with the scanned count so far.
     - any other error → `glog.Warningf` and skip the repo (per-repo gate failure, spec Failure Modes row 3).
   - `GoModParsable` = `modfile.Parse("go.mod", goMod, nil)` returned no error.
   - `branch, headSHA, err := deps.GitOps.ResolveDefaultBranch(ctx, authCloneURL)` where `authCloneURL = injectToken(normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"), deps.GhToken)`. Record `branch` and `headSHA` in a per-repo metadata map (`map[string]repoMeta{branch, headSHA}`) — do NOT add them to `ScanCandidateFacts`. On error: rate limit → `failed(...)` as above; any other error → warn and skip the repo.
   - Split the repo with `owner, name, ok := strings.Cut(repo, "/")`; when `ok` is false the repo is malformed — warn and skip it.
   - `cfgBytes, err := deps.Consent.Fetch(ctx, owner, name, branch)`; `errors.Is(err, maintainerconfig.ErrFileNotFound)` → `Consent = false`; other error → warn and skip the repo. `Consent = parsedCfg.GoUpdate.AutoUpdate`, parsing with `maintainerconfig.Parse(ctx, cfgBytes)`. A parse error → warn, `Consent = false` (no consent, same as absent).
   - Append the facts row.
4. `vulnPop, stalePop := selectPopulations(deps.Config.Allowlist, facts)`. The scan set is the union of the two, deduplicated by repo and sorted. A repo in neither population is never cloned.
5. For each repo in the scan set:
   - `select { case <-ctx.Done(): return nil, errors.Wrap(ctx, ctx.Err(), "fleet scan cancelled") default: }` at the top of every iteration.
   - `workdir := filepath.Join(os.TempDir(), workdirPrefix+"scan-"+sanitizePathComponent(repo))`; `os.RemoveAll(workdir)` before cloning (reuse the existing `workdirPrefix` and `sanitizePathComponent`; do NOT use `setupWorkdir`, which keys on the task identifier and would give every repo the same directory).
   - `deps.GitOps.CloneAtRef(ctx, authCloneURL, meta.branch, workdir)`; on error: rate limit → `failed(...)`; otherwise warn, remove the workdir, and continue (repo skipped, not counted).
   - `targets, err := detectGateTargets(ctx, workdir)`; an empty list means the repo has no scanner to run — record it as scanned with no findings.
   - For each target: `output, _, runErr := deps.Gate.RunTargetFull(ctx, workdir, target)`. Parse the output with `parseScannerOutput(target, output)` **regardless of `runErr`** (a red gate still prints its findings). Only when `runErr != nil && output == ""` is the target unusable — treat that repo as a gate failure: warn, remove the workdir, skip, not counted.
   - Union every finding's `ID` across targets into a set.
   - `suppressed, err := loadSuppressedVulnIDs(ctx, workdir)`; on error warn and treat the set as empty. Remove every suppressed ID from the union.
   - `VulnIDs` = the remaining IDs, sorted with `sort.Strings`; nil when empty.
   - Append a `ScanRepoResult{Repo, Ref: meta.headSHA, CloneURL: "git@github.com:"+repo+".git", VulnIDs}` and increment the scanned count.
   - `os.RemoveAll(workdir)` after every iteration, including the skip paths.
6. Build `FleetScanReport{ScannedAt: deps.CurrentDateTime.Now().String(), Scanned: scanned, Repos: results}`; marshal it with `agentlib.MarshalSectionTyped(ctx, "## Fleet Scan", report)` and apply it with `md.ReplaceSection(section)`. Never assemble the section with `strings.Index`.
7. Log exactly one line: `glog.Infof("fleet-scan: scanned=%d", scanned)`. Do not read wall-clock anywhere except `deps.CurrentDateTime`.
8. Return `&agentlib.Result{Status: agentlib.AgentStatusDone}` with an empty `NextPhase`. The scan task is the recurring template: an empty `NextPhase` is an in-place save that keeps the task `in_progress` so the next night's Job runs again. Never return `NextPhase: "done"`.

Keep every function inside the repo's lint limits (`funlen` 80 lines / 50 statements, `gocognit` 20). `Run` must delegate, not inline: at minimum `gateRepo(ctx context.Context, repo string) (ScanCandidateFacts, bool, error)`, `scanRepo(ctx context.Context, repo string, meta repoMeta) (ScanRepoResult, bool, error)` and `writeReport(ctx context.Context, md *agentlib.Markdown, report FleetScanReport) error`, mirroring how `planningStep.Run` delegates to `resolveAndClone`, `setupAndCleanupWorkdir` and `closeOrFailUnreachable`. Do not add `//nolint` comments.

Failure semantics, all copied from the spec: a per-repo clone or gate failure is a skip and never fails the run; a rate limit fails the run naming the repo and the `scanned=` count; an empty population logs `scanned=0` and completes (it is a config fault, not a clean fleet); the step never mutates `assignee` or `status` and never returns `NextPhase: human_review`.

4. Create `pkg/factory/fleet_scan.go`:

```go
// CreateFleetScanAgent composes the single-phase, LLM-free fleet scan agent.
func CreateFleetScanAgent(deps updatepkg.FleetScanDeps) *agentlib.Agent {
	return agentlib.NewAgent(fleetScanPhases(deps)...)
}

// fleetScanPhases is named (not an inline literal) so the composition test can
// assert the phase carries exactly one step and no Claude runner.
func fleetScanPhases(deps updatepkg.FleetScanDeps) []agentlib.Phase
```

`fleetScanPhases` returns exactly one phase: `agentlib.NewPhase(domain.TaskPhaseExecution, updatepkg.NewFleetScanStep(deps))`. It must contain no `CreateClaudeRunner` call and no other step.

5. Register the task type in `pkg/factory/factory.go`:
- Add `var taskTypeFleetScan = agentlib.TaskType("github-fleet-scan")` with a doc comment stating the literal is frozen and mirrors the CRD `trigger.task_type`.
- Add a `fleetScanDeps updatepkg.FleetScanDeps` parameter to `CreateAgentProvider` (last position) and a map entry `taskTypeFleetScan: CreateFleetScanAgent(fleetScanDeps)`.
- Leave the four existing entries and their construction untouched — their behaviour must stay byte-identical.

6. Wire both entry points (both must compile; this is the sibling-entry-point check):
- `main.go`: add three `application` fields, all `required:"false"`:

```go
Owner         string `required:"false" arg:"owner" env:"OWNER" usage:"GitHub owner whose repositories the fleet scan enumerates"`
RepoAllowlist string `required:"false" arg:"repo-allowlist" env:"REPO_ALLOWLIST" usage:"Comma-separated host/owner/repo allowlist; '*' wildcard, '!' prefix excludes"`
Stage         string `required:"false" arg:"stage" env:"STAGE" usage:"Stage stamped on every task the fleet scan files"`
```

  Add `func (a *application) buildFleetScanDeps(resolvedToken string) updatepkg.FleetScanDeps` returning `FleetScanConfig{Owner: a.Owner, Allowlist: updatepkg.ParseRepoAllowlist(a.RepoAllowlist), Stage: a.Stage}` plus `NewGitHubRepoLister(resolvedToken)`, `NewGitHubRepoFileFetcher(resolvedToken)`, `maintainerconfig.NewHTTPFetcher(resolvedToken)`, the existing GitOps/GateRunner constructors, and `libtime.NewCurrentDateTime()`. Pass it to `CreateAgentProvider` from `buildRun` (it already holds `resolvedToken`).
- `cmd/run-task/main.go`: add the same three fields and build the same deps with `a.GhToken`; pass it to its `CreateAgentProvider` call.
- `pkg/factory/factory_test.go`: add a zero-value `updatepkg.FleetScanDeps{}` argument so the existing specs still compile, and update the `"error message contains the sorted accepted-types list"` assertion in the same `CreateAgentProvider` spec from `[build-fix github-update-go healthcheck oauth-probe]` to `[build-fix github-fleet-scan github-update-go healthcheck oauth-probe]` — registering the new task type changes the provider's unknown-task error message, so that assertion fails without the edit.

7. Export the test seams in `pkg/export_test.go` (the tests live in `pkg_test`):

```go
SelectPopulations               = selectPopulations
NewGitHubRepoListerForTest      = newGitHubRepoListerWithBase
NewGitHubRepoFileFetcherForTest = newGitHubRepoFileFetcherWithBase
```

`ParseRepoAllowlist` is already exported in package `pkg`, so the external test calls it directly — add no alias for it. Add `FleetScanPhases = fleetScanPhases` to `pkg/factory/export_test.go`.

8. Regenerate the mocks: `ROOTDIR=/workspace make generate`. Confirm `mocks/repo_lister.go` and `mocks/repo_file_fetcher.go` exist and are not hand-edited.

9. Tests, `package pkg_test` unless stated otherwise. Each outer `Describe` name must contain the focus word so the focused run selects it.

`pkg/fleet_scan_population_test.go` — `Describe("FleetScanPopulation", ...)`:
- A `DescribeTable` over `(allowlist, facts, expectedInVuln, expectedInStale)` with at least these rows, each `Entry` naming the repo and the gate that excludes it:
  1. `["github.com/bborbe/*", "!github.com/bborbe/go-skeleton"]` + facts for `bborbe/ip` (go.mod present+parsable, consent) → in both.
  2. the same allowlist + facts for `bborbe/go-skeleton` (all true) → in neither; gate: allowlist exclude.
  3. the same allowlist + facts for `other/repo` (all true) → in neither; gate: allowlist wildcard.
  4. `bborbe/ip` with `Consent: false` → in neither; gate: consent.
  5. `bborbe/ip` with `GoModPresent: false` → in neither; gate: go.mod presence.
  6. `bborbe/ip` with `GoModPresent: true, GoModParsable: false, Consent: true` → in vuln, not in stale; gate: go.mod parsability.
  7. a nil allowlist + facts for two consenting repos → both in both (allow-all within the owner).
- `ParseRepoAllowlist` table: `""` → nil; `" a , b "` → `["a","b"]`; `"github.com/bborbe/*,!github.com/bborbe/go-skeleton"` → two entries; `",,"` → nil.

`pkg/fleet_source_test.go` — `Describe("FleetScanSource", ...)`, driving `pkg.NewGitHubRepoListerForTest` / `pkg.NewGitHubRepoFileFetcherForTest` against `httptest.NewServer`:
- list returns the two pages' `full_name` values, sorted, and requests page 1 then page 2;
- list 403 → `errors.Is(err, pkg.ErrFleetRateLimited)`;
- fetch returns the decoded bytes of a base64 file;
- fetch 404 → `errors.Is(err, pkg.ErrFleetFileNotFound)`;
- fetch 500 → wrapped error containing `status 500` and NOT the raw body;
- fetch 429 → `errors.Is(err, pkg.ErrFleetRateLimited)`.

`pkg/steps_fleet_scan_test.go` — `Describe("FleetScanStep", ...)`, using `mocks.RepoLister`, `mocks.RepoFileFetcher`, `mocks.MaintainerConfigFetcher`, `mocks.GitOps`, `mocks.GateRunner`:
- happy path: two consenting repos, one with a finding and one without → `scanned=2`, one `ScanRepoResult` carrying the finding ID, the suppressed ID removed, the `## Fleet Scan` section present with `scanned_at` from an injected `libtime.CurrentDateTimeGetterFunc` (never the real clock);
- empty `Owner` → `NeedsInput` naming OWNER;
- empty repo list → `Done`, `scanned=0`, section written;
- lister error → `Failed`;
- `FetchFile` rate limit on the second repo → `Failed` whose message names the repo and `scanned=0`;
- `CloneAtRef` error for one repo → the other still scanned, run `Done`, `scanned` = 1;
- a gate target that returns a non-empty output plus an error still contributes its findings;
- `ctx` cancelled before the loop → `Run` returns a non-nil error;
- the returned `Result.NextPhase` is empty and the status is `Done` (the template must stay alive).
Use unique repo names per spec: `scanWorkdir` is derived from `os.TempDir()` and `sanitizePathComponent`, so a shared repo name lets specs destroy each other's clone. Fixture each workdir through `mocks.GitOps.CloneAtRefStub`: `os.MkdirAll(workdir, 0o755)` and write the fixture `Makefile` (and, where the spec needs them, `go.mod` and the suppression surfaces) into it, exactly as `pkg/steps_planning_test.go` does — a mocked clone that creates no files leaves `detectGateTargets` with nothing to detect, so the spec would see no findings.

`pkg/factory/fleet_scan_test.go` — `Describe("FleetScanFactory", ...)`:
- `factory.FleetScanPhases(updatepkg.FleetScanDeps{})` returns one phase named `domain.TaskPhaseExecution` whose `Steps` has length 1 and whose only step's `Name()` is `"fleet-scan"`;
- `provider.Get(ctx, agentlib.TaskType("github-fleet-scan"))` returns a non-nil agent and no error;
- in `pkg/factory/factory_test.go`, extend the existing `CreateAgentProvider` spec so `github-update-go`, `build-fix`, `healthcheck` and `oauth-probe` still all resolve.

Every new `.go` file carries the project's BSD license header (copy it from `pkg/steps_fix_execution.go`).
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- One binary, one image: the scan is a new task type in `pkg/factory`'s provider map, not a new repo or image.
- Reuse `detectGateTargets`, `parseScannerOutput`, `loadSuppressedVulnIDs`, `injectToken`, `normalizeCloneURLToHTTPS`, the `git.GitOps` seam and the `GateRunner` seam. Do not re-port them.
- Reuse `github.com/bborbe/maintainer/repoallowlist` for allowlist matching. Do not write a matcher.
- No `time.Now()`: inject `libtime.CurrentDateTimeGetter` and read the clock only through it.
- Check `ctx.Done()` inside the per-repo loop.
- Read and write task sections through `MarshalSectionTyped` / `ExtractSection`, never `strings.Index`.
- Guard `result == nil`.
- On failure emit `Status: failed` or `needs_input`; never `NextPhase: human_review`, never mutate `assignee` or `status`.
- The live `github-update-go`, `build-fix` and `healthcheck` behaviour must stay byte-identical — the scan gets its own Config CR on the same image.
- Repos outside the configured allowlist are out of scope; no caller-supplied repo reaches a clone.
- The GitHub token is never logged.
- Errors go through `github.com/bborbe/errors` with `ctx`, never `fmt.Errorf`.
- Never hand-edit generated mocks; regenerate them with `make generate`.
- Do NOT touch `.github/workflows/`.
- No new dependency: `repoallowlist`, `maintainerconfig`, `golang.org/x/mod/modfile`, `github.com/google/uuid` and the standard library are all already required.
- No new scenario: the population, seams and step are fully covered by unit tests with in-process HTTP servers and counterfeiter fakes.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must exit 0.

The population and step suites pass and actually ran:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='FleetScan' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-pkg.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<10){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-pkg.log
```

The factory suite passes and the new task type resolves:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/factory/ -run TestSuite -count=1 -args -ginkgo.focus='FleetScan|CreateAgentProvider' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-factory.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<5){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-factory.log
```

Confirm the seams and generated mocks exist:

```bash
grep -c 'func NewFleetScanStep' pkg/steps_fleet_scan.go          # expect: 1
grep -c 'func NewGitHubRepoLister' pkg/fleet_source.go           # expect: >= 1
grep -c 'func NewGitHubRepoFileFetcher' pkg/fleet_source.go      # expect: >= 1
grep -c 'func (fake \*RepoLister) ListRepos' mocks/repo_lister.go                 # expect: 1
grep -c 'func (fake \*RepoFileFetcher) FetchFile' mocks/repo_file_fetcher.go      # expect: 1
grep -c 'taskTypeFleetScan' pkg/factory/factory.go               # expect: >= 2
grep -c 'repoallowlist.IsAllowed' pkg/fleet_scan_population.go   # expect: 1
```

Confirm the scan phase carries no model call:

```bash
! grep -q 'CreateClaudeRunner' pkg/factory/fleet_scan.go
grep -c 'NewPhase' pkg/factory/fleet_scan.go                      # expect: 1
```

Confirm both entry points were updated:

```bash
grep -c 'buildFleetScanDeps' main.go                              # expect: >= 2
grep -c 'CreateAgentProvider' cmd/run-task/main.go                # expect: 1
```

Self-check before finishing: walk every `<summary>` bullet and every requirement against the diff; re-run the focused commands and `make precommit`.
</verification>

<!--
Open questions carried from the spec (the spec is silent; the choices below are
deliberate and must not be silently reversed):

1. Gate facts are read over the GitHub contents API, not from a clone. The spec
   says "read go.mod presence/parsability" and "clone it" as separate stages, and
   cloning every allowlisted repo nightly would defeat the change's whole point.
   If the execution agent finds the contents API cannot serve go.mod for a repo
   the App can clone, fall back to a clone-first gate and note it in the PR.

2. The vuln scan runs EVERY target detectGateTargets returns, and parses the
   output even when the target exits non-zero. The spec says "run its gate
   targets"; its assumptions name govulncheck/trivy/osv-scanner, not `precommit`.
   A target whose output is empty on failure is treated as a per-repo skip.

3. `stage` is injected from a new STAGE env var. The spec puts the Config CR and
   the Schedule CR out of scope, and AC5 names `stage` as part of the envelope,
   so the value must come from the Job environment. Empty stage omits the key
   (agentlib then reads it as "prod" — the deploy must set STAGE explicitly).

4. `scanned=` counts repos that were cloned and scanned successfully, so a skipped
   repo makes it lower than the allowlist population, as Failure Modes row 3
   requires.
-->
