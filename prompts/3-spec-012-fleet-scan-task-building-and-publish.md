---
spec: ["012-nightly-fleet-scan"]
status: draft
created: "2026-11-26T08:42:49Z"
---

# Fleet scan: task building, frozen identifiers, publishing and idempotency

<summary>
- Every repository the scan finds work for gets one task per task family, never a shared one.
- The identifier of each task comes from the retired producers' frozen namespaces, so a migration does not re-file a single open work item.
- Reordering the advisory list yields the same identifier, and the same input always yields the same identifier.
- The two task families carry the same envelope and differ only in identifier, title, body and the advisory list.
- A vulnerability task names the advisories and their count; a stale-ignore task carries neither field.
- Every built task passes the same validation the real publish path applies, so nothing is rejected downstream.
- The run asks the task store whether an identifier already exists and does not publish it again when it does.
- The run reports how many tasks it filed and how many were already present.
- A publish failure stops the run and reports how many tasks were filed before it.
- The publish path is a seam, so the run is testable without Kafka.
</summary>

<objective>
Turn the scan's per-repository findings into publishable tasks with the frozen identifier contracts, publish them through the controller's create-task path, and make a second run over an unchanged fleet a no-op that reports the already-present count.
</objective>

<context>
This prompt runs third in spec 012. Prompts 1 (`1-spec-012-fleet-scan-register-and-population.md`) and 2 (`2-spec-012-fleet-scan-stale-classification.md`) land first and create `pkg/fleet_source.go`, `pkg/fleet_scan_population.go`, `pkg/steps_fleet_scan.go` (the step, `FleetScanDeps`, `ScanRepoResult` with `VulnIDs` and `StaleIDs`, `FleetScanReport`, the per-repo scan loop), `pkg/fleet_scan_stale.go`, and the registered `github-fleet-scan` task type with both entry points wired. Read all of them before changing anything. If any of those files is missing, stop and report that the sibling prompt has not run — do not recreate them here.

Read these files before writing anything:
- `pkg/steps_fleet_scan.go` — the `Run` you extend and the `ScanRepoResult` / `FleetScanReport` types.
- `pkg/factory/factory.go` — `CreateBuildFixChainEmitter` and `buildChainCommand`: the exact pattern for building a `task.CreateCommand` (title, identifier, frontmatter, body) and for publishing it through `cdb.NewCommandObjectSender` + `task.NewCreateCommandSender`. Also `computeChainTitle`, `shortSHA`, `chainFrontmatter`, `splitRepo`, and the `updateGoChainNamespace` doc comment on why a distinct namespace exists.
- `pkg/fleet_scan_stale.go` — `renderStaleBody(ids []string) string`, which the stale command's body uses.
- `docs/design.md` § 3.3 — the task frontmatter envelope (`task_type`, `assignee`, `phase`, `status`, `stage`, `task_identifier`, `title`, `repo`, `clone_url`, `ref`, optional `vulns` / `vuln_count`).
- `main.go` — `buildChainEmitter`, `buildRun` and its single `cleanup` closure. The new publish path follows `buildChainEmitter`'s shape (own producer, own closer, never share a producer with another component — sarama's `AsyncClose` is not idempotent and a second `Close` panics).
- `cmd/run-task/main.go` — the local entry point, which wires no Kafka producer.
- `pkg/export_test.go`, `pkg/factory/export_test.go`, `pkg/pkg_suite_test.go`, `pkg/factory/factory_suite_test.go`.
- `pkg/steps_fleet_scan_test.go` — the step specs you extend.

Library facts (already verified; use exactly these):
- `github.com/bborbe/agent/command/task`:
  - `task.CreateCommand{TaskIdentifier lib.TaskIdentifier; Title string; Frontmatter lib.TaskFrontmatter; Body string; TargetVault string}`.
  - `func (cmd CreateCommand) Validate(ctx context.Context) error` — rejects an empty title, a title over 200 runes, `/ \ : * ? " < > |` in the title, a body over 500 KiB, and a malformed `targetVault`.
  - `task.CreateCommandSender` has `SendCommand(ctx context.Context, cmd CreateCommand) error`.
  - `task.NewCreateCommandSender(commandObjectSender cdb.CommandObjectSender, defaultVault string) CreateCommandSender`.
- `github.com/bborbe/agent` — `agentlib.TaskFrontmatter` is `map[string]interface{}`; `agentlib.TaskIdentifier` is a string type.
- `github.com/google/uuid` — `uuid.NewSHA1(space UUID, data []byte) UUID`; `uuid.MustParse(string) UUID`. The existing `updateGoChainNamespace` in `pkg/factory/factory.go` shows the package-level `uuid.MustParse` pattern.
- `cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory)` — see `CreateBuildFixChainEmitter`.
- `github.com/bborbe/log` — `log.DefaultSamplerFactory`.

Read the coding guides (in-container paths):
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-context-cancellation-in-loops.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md`
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-licensing-guide.md`

Read the approved spec `specs/in-progress/012-nightly-fleet-scan.md`: Desired Behavior 4-5, Constraints (the frozen namespaces and both seed formats), Failure Modes rows 4 and 6, Security/Abuse Cases, and Acceptance Criteria 4, 5, 6.

This run uses `workflow: worktree`, so the execution container masks the repo's `.git`. This prompt needs no git commands. Run `ROOTDIR=/workspace make precommit` for verification.
</context>

<requirements>
1. Create `pkg/fleet_scan_identifiers.go` with the frozen identifier contract:

```go
// scanVulnNamespace is the retired github-vuln-watcher's frozen v5 UUID
// namespace for vuln-drift task identifiers. Never mint a new one: an existing
// open task must keep its identifier across the migration.
const scanVulnNamespace = "5c3bcb6b-fb0f-4c61-a4c3-8a17fd037f52"

// scanStaleNamespace is the retired github-update-go-watcher's frozen v5 UUID
// namespace for stale-ignore task identifiers.
const scanStaleNamespace = "4f2a7c1e-9b3d-4c58-8e21-6d0b7a5c9f13"

// scanStaleGeneration is the generation the nightly scan files at. The frozen
// seed carries a generation so a future re-filing scheme can mint a new
// identifier without colliding; the nightly run always uses 1.
const scanStaleGeneration = 1

// DeriveScanVulnTaskID returns the deterministic identifier for a repo's
// vulnerability task. The seed is the retired watcher's verbatim:
//
//	vuln-drift-<owner>-<repo>-<comma-joined deduped sorted ids>
//
// A reordered or repeated advisory list yields the same identifier.
func DeriveScanVulnTaskID(owner, repo string, ids []string) string

// DeriveScanStaleIgnoreTaskID returns the deterministic identifier for a
// repo's stale-ignore task. The seed is the retired watcher's verbatim:
//
//	stale-ignore-<owner>-<repo>-g<generation>-<comma-joined sorted ids>
//
// The list is sorted but NOT deduped — deduping would change the identifier of
// an already-filed item.
func DeriveScanStaleIgnoreTaskID(owner, repo string, generation int, ids []string) string
```

Both functions parse their namespace once at package level with `uuid.MustParse` into a package var, sort a copy of `ids` with `sort.Strings`, and return `uuid.NewSHA1(ns, []byte(seed)).String()`. `DeriveScanVulnTaskID` additionally removes duplicates after sorting (`slices.Compact` on the sorted copy). `generation` is rendered with `strconv.Itoa`.

2. Create `pkg/fleet_scan_commands.go`:

```go
// buildScanCommands renders one CreateCommand per (repo, family) that has work.
// A repo carrying both a vuln set and a stale set yields two commands.
func buildScanCommands(stage string, results []ScanRepoResult) []task.CreateCommand

// scanFrontmatter builds the watchers' envelope shared by both families.
func scanFrontmatter(repo, ref, stage string) agentlib.TaskFrontmatter

// scanTaskTitle renders "<prefix> <owner>-<repo> <sha:7>". The dash form is
// required: task.CreateCommand.Validate rejects '/' in a title.
func scanTaskTitle(prefix, repo, ref string) string

func scanShortSHA(ref string) string
func splitRepoName(repo string) (owner, name string)
```

Rules:
- `scanFrontmatter` returns a map with exactly: `task_type: "github-update-go"`, `assignee: "github-update-go-agent"`, `phase: "planning"`, `status: "in_progress"`, `repo: <repo>`, `clone_url: "git@github.com:"+repo+".git"`, `ref: <ref>`, and `stage: <stage>` only when `stage` is non-empty. Copy the key names and value shapes from `chainFrontmatter` in `pkg/factory/factory.go`; do not invent extra keys.
- The vuln command: identifier `DeriveScanVulnTaskID(owner, name, result.VulnIDs)`; title `scanTaskTitle("Update Go", result.Repo, result.Ref)`; frontmatter from `scanFrontmatter` plus `vulns` (the `[]string` of IDs) and `vuln_count` (`len(result.VulnIDs)` as an `int`); body a short operator header naming the count and listing every ID on its own bullet.
- The stale command: identifier `DeriveScanStaleIgnoreTaskID(owner, name, scanStaleGeneration, result.StaleIDs)`; title `scanTaskTitle("Remove stale vuln ignores", result.Repo, result.Ref)`; frontmatter from `scanFrontmatter` with NO `vulns` and NO `vuln_count`; body `renderStaleBody(result.StaleIDs)`.
- `TargetVault` stays empty (the controller's default vault), matching `buildChainCommand`.
- Emit the vuln command before the stale command for the same repo; preserve the `results` order otherwise. Skip a family whose ID list is empty.
- Every returned command must pass `cmd.Validate(ctx)`; the tests assert this for every command the builder produces.

3. Create `pkg/fleet_scan_task_store.go`:

```go
//counterfeiter:generate -o ../mocks/scan_command_sender.go --fake-name ScanCommandSender . ScanCommandSender

// ScanCommandSender is the publish seam. task.CreateCommandSender satisfies it
// structurally, so the production wiring passes the real sender.
type ScanCommandSender interface {
	SendCommand(ctx context.Context, cmd task.CreateCommand) error
}

//counterfeiter:generate -o ../mocks/scan_task_store.go --fake-name ScanTaskStore . ScanTaskStore

// ScanTaskStore is the controller boundary: it answers whether an identifier is
// already present and files a task when it is not.
type ScanTaskStore interface {
	// Has reports whether a task with this identifier already exists.
	Has(ctx context.Context, identifier agentlib.TaskIdentifier) (bool, error)
	// Publish files one task.
	Publish(ctx context.Context, cmd task.CreateCommand) error
}

// NewKafkaScanTaskStore returns the production store over the controller's
// create-task sender.
//
// Has currently reports false: the controller's create handler is the only
// place that knows whether a title path is occupied, and it signals a collision
// on the result topic, which this Job does not consume. The controller's
// title-path dedup still makes a re-publish a no-op, so the fleet never gains a
// duplicate task; only the already_recorded count under-reports until a task
// lookup path exists.
func NewKafkaScanTaskStore(sender ScanCommandSender) ScanTaskStore
```

`Publish` wraps the sender error with `errors.Wrap(ctx, err, "publish fleet scan task")`; `Has` returns `(false, nil)`. Neither logs the command body.

4. Extend the step in `pkg/steps_fleet_scan.go`:
- Add `Store ScanTaskStore` to `FleetScanDeps`.
- Add `Filed int \`json:"filed"\`` and `AlreadyRecorded int \`json:"already_recorded"\`` to `FleetScanReport`.
- After the scan loop and before the report is written, publish:
  - `commands := buildScanCommands(deps.Config.Stage, results)`.
  - When `deps.Store == nil` (local replay with no Kafka producer): log `glog.V(2).Infof("fleet-scan: publish disabled — %d command(s) not filed", len(commands))` and leave both counts at zero. Do not invent counts.
  - Otherwise, for each command in order: check `ctx.Done()`; `has, err := deps.Store.Has(ctx, cmd.TaskIdentifier)`; on error return `failed(fmt.Sprintf("fleet-scan: task lookup failed after filed=%d: %v", filed, err))`; when `has` is true increment `already` and continue (do NOT publish); otherwise `err := deps.Store.Publish(ctx, cmd)`; on error return `failed(fmt.Sprintf("fleet-scan: publish failed after filed=%d: %v", filed, err))`; on success increment `filed`.
- Set `report.Filed` and `report.AlreadyRecorded` before marshalling the `## Fleet Scan` section.
- Extract `publishCommands(ctx context.Context, store ScanTaskStore, commands []task.CreateCommand) (filed, already int, res *agentlib.Result)` so `Run` stays inside the repo's `funlen` 80-line / 50-statement and `gocognit` 20 limits. `Run` keeps the `store == nil` branch and passes `deps.Store` only on the non-nil path; a non-nil `res` is returned by `Run` verbatim. Do not add `//nolint` comments.
- Replace the prompt-1 log line with exactly: `glog.Infof("fleet-scan: scanned=%d filed=%d already_recorded=%d", scanned, filed, already)`.

5. Wire the publish path:
- Create `pkg/factory/fleet_scan_store.go`:

```go
// CreateKafkaScanTaskStore wires the fleet scan's publish path over the
// controller's Kafka command bus, exactly as CreateBuildFixChainEmitter does.
// The producer is passed in pre-built; main.go is its single close owner.
func CreateKafkaScanTaskStore(
	syncProducer libkafka.SyncProducer,
	topicPrefix base.TopicPrefix,
) updatepkg.ScanTaskStore
```

  Body: `sender := cdb.NewCommandObjectSender(syncProducer, topicPrefix, log.DefaultSamplerFactory)`, `createSender := task.NewCreateCommandSender(sender, "")`, `return updatepkg.NewKafkaScanTaskStore(createSender)`.
- Change `main.go`'s `buildFleetScanDeps` (added by prompt 1) to take the store as a second parameter — `buildFleetScanDeps(resolvedToken string, store updatepkg.ScanTaskStore)` — and set `FleetScanDeps.Store` from it.
- `main.go`: add `func (a *application) buildScanTaskStore(ctx context.Context) (updatepkg.ScanTaskStore, func(), error)`, mirroring `buildChainEmitter`: no brokers → `(nil, func(){}, nil)`; otherwise `factory.CreateSyncProducer(ctx, a.KafkaBrokers)`, return the store plus a closer that calls `producer.Close()` and logs a warning on failure. Call it from `buildRun`, put the store into `buildFleetScanDeps`, and close it from `buildRun`'s `cleanup` closure (after the deliverer's and the chain producer's closers). Do not share a producer between the chain emitter and the scan store.
- `cmd/run-task/main.go`: pass a nil store (local mode logs the intended commands instead of publishing).

6. Tests, `package pkg_test` unless stated otherwise.

`pkg/fleet_scan_identifiers_test.go` — outer `Describe("ScanTaskID derivation", ...)` so the focus word matches the spec's evidence command:
- `DeriveScanVulnTaskID("bborbe", "ip", []string{"GO-2026-0001", "CVE-2026-9999"})` equals exactly `"5b8f26d2-bae1-538d-b245-f0a81db06c23"`.
- `DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, []string{"GO-2026-0002"})` equals exactly `"064c07cc-2d45-5b18-af93-c4c9b2741f40"`.
- the reordered vuln list `[]string{"CVE-2026-9999", "GO-2026-0001"}` yields the same vuln UUID.
- the same vuln input twice yields the same UUID; the same stale input twice yields the same UUID.
- `DeriveScanStaleIgnoreTaskID("bborbe", "ip", 2, []string{"GO-2026-0002"})` differs from the generation-1 UUID.
- a duplicated vuln ID (`["GO-2026-0001","GO-2026-0001"]`) yields the same UUID as the single ID (dedup).
- a duplicated stale ID (`["GO-2026-0002","GO-2026-0002"]`) differs from the single ID (no dedup) — this pins the frozen asymmetry.
- the two families never collide for the same repo and ID.

`pkg/fleet_scan_commands_test.go` — outer `Describe("FleetScanCommands", ...)`:
- a `ScanRepoResult` carrying both `VulnIDs` and `StaleIDs` produces exactly two commands;
- for that pair, `task_type`, `assignee`, `phase`, `status`, `stage`, `repo`, `clone_url` and `ref` are equal; `task_identifier` differs;
- the vuln command's frontmatter carries `vulns` equal to the ID list and `vuln_count` equal to its length; the stale command's frontmatter carries neither key (`_, ok := fm["vulns"]` is false, same for `vuln_count`);
- every produced command passes `cmd.Validate(context.Background())`;
- neither title contains `/`;
- a result with only `VulnIDs` produces one command, and one with only `StaleIDs` produces one;
- a result with neither produces none;
- `stage` is omitted from the frontmatter when the configured stage is empty and present when it is not, in both families.

`pkg/fleet_scan_task_store_test.go` — outer `Describe("FleetScanTaskStore", ...)`:
- `NewKafkaScanTaskStore(fake)` with `mocks.ScanCommandSender`: `Publish` forwards the exact command to `SendCommandCalls()[0].Cmd`;
- a sender error is returned wrapped and `errors.Is` still matches the sentinel;
- `Has` returns `(false, nil)`.

Step-level, extending `Describe("FleetScanStep", ...)` in `pkg/steps_fleet_scan_test.go`, using `mocks.ScanTaskStore`:
- **second run over an unchanged fleet files nothing** (Acceptance Criterion 6): drive `Run` twice against the same fakes. Use a stateful store stub — `HasStub` reads a `map[agentlib.TaskIdentifier]bool` that `PublishStub` writes into. The first drive publishes M commands and logs `scanned=N filed=M already_recorded=0`; the second drive calls `Publish` zero times (`len(PublishCalls())` unchanged), logs `scanned=N filed=0 already_recorded=M`, and its returned command count is zero. Capture both log lines with a `glog` sink if the suite already has one; otherwise assert the two counts from the report section (`Filed`, `AlreadyRecorded`, `Scanned`) and from `PublishCalls()`.
- a `Has` error fails the run with a message naming the filed count so far;
- a `Publish` error on the second of three commands fails the run with `filed=1` in the message and does not report success;
- a nil `Store` completes `Done` with `filed=0` and `already_recorded=0`.

Every new `.go` file carries the project's BSD license header (copy it from `pkg/fleet_scan_stale.go`).

7. Export the command builder for the external tests: add `BuildScanCommands = buildScanCommands` and `ScanFrontmatter = scanFrontmatter` to `pkg/export_test.go` (the command specs live in `pkg_test` and construct `[]pkg.ScanRepoResult` directly).

8. Regenerate the mocks: `ROOTDIR=/workspace make generate`. Confirm `mocks/scan_command_sender.go` and `mocks/scan_task_store.go` exist and are not hand-edited.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Existing tests must still pass.
- The frozen namespaces are copied verbatim: `5c3bcb6b-fb0f-4c61-a4c3-8a17fd037f52` (vuln-drift) and `4f2a7c1e-9b3d-4c58-8e21-6d0b7a5c9f13` (stale-ignore). Never mint a new one.
- The two seed formats are copied verbatim and must not drift:
  - vuln: `vuln-drift-<owner>-<repo>-<comma-joined deduped sorted ids>`
  - stale: `stale-ignore-<owner>-<repo>-g<generation>-<comma-joined sorted ids>` (sorted, **not** deduped)
- One binary, one image: the publish path reuses the controller's existing create-task command, not a new topic or a new service.
- Do NOT change `pkg/factory/factory.go`'s existing chain emitter, the updater phases, the build-fix phases, or any existing task type. The new publish path gets its own producer and its own closer.
- Reuse `OSVFetcher`, `IgnoreClassifier`, `parseVulnsList` and the existing GitHub client. Do not re-port them.
- No `time.Now()`: any clock read goes through the injected `libtime.CurrentDateTimeGetter`.
- Check `ctx.Done()` inside the per-repo loop and the publish loop.
- Read and write task sections through `MarshalSectionTyped` / `ExtractSection`, never `strings.Index`.
- Guard `result == nil`.
- On failure emit `Status: failed` or `needs_input`; never `NextPhase: human_review`, never mutate `assignee` or `status`.
- The live `github-update-go`, `build-fix` and `healthcheck` behaviour must stay byte-identical.
- The scan writes only task records; it never pushes, merges or edits a repository.
- Clone destinations are derived from the repo name under `os.TempDir()`, never from a caller-controlled path.
- The GitHub token is never logged.
- Errors go through `github.com/bborbe/errors` with `ctx`, never `fmt.Errorf`.
- Never hand-edit generated mocks; regenerate them with `make generate`.
- Do NOT touch `.github/workflows/`.
- No new dependency.
- No new scenario: the identifiers, the command envelope and the idempotency contract are fully covered by unit tests with counterfeiter fakes. The real Kafka hop and the nightly Job belong to the `bborbe/nuke` manifest change, outside this spec.
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` — must exit 0.

Acceptance Criterion 4 — the golden identifiers, named in the output:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='ScanTaskID' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-ids.log
grep -q '5b8f26d2-bae1-538d-b245-f0a81db06c23' /tmp/fleet-scan-ids.log || { echo 'FAIL: vuln UUID missing'; exit 1; }
grep -q '064c07cc-2d45-5b18-af93-c4c9b2741f40' /tmp/fleet-scan-ids.log || { echo 'FAIL: stale UUID missing'; exit 1; }
awk '/Ran [0-9]+ of/{n=$2; if(n+0<7){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-ids.log
```

The command envelope and idempotency suites pass:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='FleetScanCommands|FleetScanTaskStore|FleetScanStep' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-publish.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<10){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-publish.log
```

The whole fleet-scan family still passes:

```bash
set -o pipefail
ROOTDIR=/workspace go test -mod=mod ./pkg/ -run TestSuite -count=1 -args -ginkgo.focus='FleetScan|ScanTaskID' -ginkgo.v 2>&1 | tee /tmp/fleet-scan-all.log
awk '/Ran [0-9]+ of/{n=$2; if(n+0<25){print "FAIL: ran only "n" focused specs"; exit 1}}' /tmp/fleet-scan-all.log
```

Confirm the frozen contracts and the publish seam:

```bash
grep -c '5c3bcb6b-fb0f-4c61-a4c3-8a17fd037f52' pkg/fleet_scan_identifiers.go   # expect: 1
grep -c '4f2a7c1e-9b3d-4c58-8e21-6d0b7a5c9f13' pkg/fleet_scan_identifiers.go   # expect: 1
grep -c '"vuln-drift-"' pkg/fleet_scan_identifiers.go                          # expect: 1
grep -c '"stale-ignore-"' pkg/fleet_scan_identifiers.go                        # expect: 1
grep -c 'func (fake \*ScanTaskStore) Has' mocks/scan_task_store.go             # expect: 1
grep -c 'func (fake \*ScanCommandSender) SendCommand' mocks/scan_command_sender.go  # expect: 1
grep -c 'already_recorded=%d' pkg/steps_fleet_scan.go                          # expect: 1
grep -c '^func CreateKafkaScanTaskStore' pkg/factory/fleet_scan_store.go       # expect: 1
grep -c 'buildScanTaskStore' main.go                                           # expect: >= 2
! grep -q 'NewKafkaScanTaskStore' cmd/run-task/main.go
```

Confirm the existing producers were not disturbed:

```bash
grep -c 'func CreateBuildFixChainEmitter' pkg/factory/factory.go   # expect: 1
grep -c 'func updaterPhases' pkg/factory/factory.go               # expect: 1
grep -c 'func buildFixPhases' pkg/factory/factory.go              # expect: 1
```

Self-check before finishing: walk every `<summary>` bullet and every requirement against the diff; re-run the focused commands and `make precommit`.
</verification>

<!--
Open questions carried from the spec (the spec is silent or in tension with the
code that exists; the choices below are deliberate and must not be silently
reversed):

1. `already_recorded` has no synchronous production source. The controller
   dedups on the vault TITLE PATH and signals a collision on the result topic,
   which this Job does not consume; there is no task-lookup API in this binary.
   `NewKafkaScanTaskStore.Has` therefore reports false in production, so the
   controller absorbs duplicates while the run under-reports the count. AC6 is
   satisfied through the fake store. FOLLOW-UP (not this spec): add a task
   lookup path (controller query API or Kafka request/reply) and make Has real.
   Do not fake it by reading the previous run's section — the spec forbids a
   cursor and a wall-clock decision.

2. Titles are not pinned by the spec. The chosen form is
   "<prefix> <owner>-<repo> <sha:7>" (the dash form Validate requires), because
   the controller dedups on the title path: an unchanged fleet keeps the same
   HEAD sha and therefore the same title (no duplicate), while a moved HEAD
   legitimately files a fresh task. The IDENTIFIER stays sha-free per the frozen
   seed contract.

3. The two families share the whole envelope including `ref`, so a repo needing
   both files two tasks that differ only in identifier, title, body and the
   optional vuln fields — exactly AC5.

4. `TargetVault` is left empty (controller default), matching the existing chain
   emitter; AC5 does not list it.
-->
