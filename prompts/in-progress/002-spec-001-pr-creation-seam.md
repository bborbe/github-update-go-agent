---
status: approved
spec: [001-configurable-pr-target]
created: "2026-08-15T01:30:00Z"
queued: "2026-08-15T08:16:38Z"
branch: dark-factory/configurable-pr-target
---

# Parameterize the PR-creation seam on the configured target and wire it from config

<summary>
- The pull-request creation seam stops hardcoding "draft" and takes the configured target as an argument.
- The seam is renamed so its name no longer implies draft-only.
- The draft flag is passed to the GitHub CLI only when the target is draft; with a ready target the flag is absent.
- A new deployment setting is read from the environment alongside the agent's existing settings and shows up in the generated usage output.
- An unrecognised value stops the agent at startup, before any work is done, with an error naming the value and the accepted set.
- With the setting absent, the agent behaves exactly as the previous release: it opens drafts.
- Both entry points — the Kafka job binary and the local CLI binary — read and pass the setting.
- The generated test double for the seam is regenerated, not hand-edited.
- The agent still cannot ready an existing pull request and still cannot merge.
- The review phase is untouched by this prompt; it still expects draft-ness unconditionally.
</summary>

<objective>
Make the pull-request creation seam take the configured `PRTarget` as an argument instead of hardcoding the draft flag, and thread that value from a new `PR_TARGET` deployment setting through both binaries and the factory into the execution step — so an operator can choose, per deployment, whether the agent opens drafts or ready-for-review pull requests. With the setting absent the agent must behave byte-identically to today.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read fully before changing anything:
- `pkg/pr_target.go` — the `PRTarget` type added by the previous prompt (`PRTargetDraft`, `PRTargetReady`, `IsDraft()`, `ParsePRTarget`). If this file does not exist, STOP: prompt 1 has not shipped.
- `pkg/gh_cli.go` — the `GhCli` interface, `NewOSExecGhCli`, `osExecGhCli.CreateDraftPR`, `cmdEnv`, `lastNonEmptyLine`.
- `pkg/steps_execution.go` — `executionStep`, `NewExecutionStep`, and the `s.gh.CreateDraftPR(...)` call site inside `Run`.
- `pkg/factory/factory.go` — `CreateAgent`, `CreateAgentProvider`, `CreateGhCli`.
- `main.go` — the `application` struct tags, `Run`, `buildClaudeEnv`, `prepareAuth`.
- `cmd/run-task/main.go` — the second entry point with its own `application` struct and `Run`, calling `factory.CreateAgent` directly.
- `pkg/steps_execution_test.go` — the existing execution-step tests using `mocks.GhCli` (`CreateDraftPRReturns`, `CreateDraftPRArgsForCall`, `CreateDraftPRCallCount`).
- `pkg/factory/factory_test.go` — two call sites of `factory.CreateAgentProvider` / `factory.CreateAgent`.
- `pkg/export_test.go` — the existing pattern for exposing unexported helpers to `pkg_test`.
- `Makefile.precommit` — the `generate` target (`rm -rf mocks`, `go generate -mod=mod ./...`) that regenerates counterfeiter fakes.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — factories stay zero-logic, `Create*` prefix.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` — counterfeiter fakes are generated, never hand-edited.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits, notably `funlen` (80 lines / 50 statements).
</context>

<requirements>
1. **Rename and parameterize the seam** in `pkg/gh_cli.go`. Replace the `CreateDraftPR` interface method with:

   ```go
   // CreatePR opens a pull request from head against base, running gh inside
   // workdir so the repo is inferred from the git remote. target selects
   // draft or ready-for-review at creation time. Returns the PR URL.
   CreatePR(ctx context.Context, workdir, base, head, title, body string, target PRTarget) (string, error)
   ```

   `target` is the last parameter. Keep `FindOpenPRByHead` and `ViewPR` unchanged. Keep the `//counterfeiter:generate` directive unchanged.

2. **Rewrite the `GhCli` interface doc comment** (currently: "Three methods cover the agent's entire PR surface: create a DRAFT PR…" and "Deliberately absent (design § 7.0): no Ready, no Merge — the agent never flips a draft and never merges; the human does."). The replacement must state that the creation target is configurable per deployment, and that the agent still has no Ready and no Merge capability: it never changes the draft-ness of an already-open pull request and never merges. Do NOT leave the words "never flips a draft" in the file — spec AC9 lists this comment as one of the eight sites that must stop asserting the unconditional claim.

3. **Extract the argv builder** so the subprocess boundary is unit-testable:

   ```go
   // prCreateArgs builds the argv for `gh pr create`. --draft is included
   // only when the target is draft; a ready target omits the flag entirely.
   func prCreateArgs(base, head, title, body string, target PRTarget) []string
   ```

   It returns `[]string{"pr", "create", "--draft", "--base", base, "--head", head, "--title", title, "--body", body}` for a draft target, and the same slice without `"--draft"` for a ready target. Keep the existing flag order.

4. **Reimplement `osExecGhCli.CreatePR`** to call `exec.CommandContext(ctx, "gh", prCreateArgs(base, head, title, body, target)...)`, keeping `cmd.Dir = workdir`, `cmd.Env = g.cmdEnv()` and the existing `#nosec G204` comment (update its wording if it mentions the draft flag). On failure it must keep surfacing the gh output verbatim — `errors.Errorf(ctx, "gh pr create (target=%s): %s", target, strings.TrimSpace(string(out)))` or equivalent. This is the spec's failure mode "credential lacks permission to open a non-draft pull request": the underlying gh error text must not be swallowed. Update the `glog.V(2)` success line to name the target instead of hardcoding `--draft`, and delete the `// --draft is hardcoded: the agent opens drafts only; the human promotes.` comment.

5. **Expose the argv builder to tests** by adding `PRCreateArgs = prCreateArgs` to the existing `var (...)` block in `pkg/export_test.go`.

6. **Thread the target into the execution step** in `pkg/steps_execution.go`:
   - Add a `prTarget PRTarget` field to `executionStep`.
   - `NewExecutionStep(runner claudelib.ClaudeRunner, ops git.GitOps, gh GhCli, gate GateRunner, ghToken string, prTarget PRTarget) agentlib.Step` — `prTarget` is the last parameter; set the field in the struct literal.
   - Replace the `s.gh.CreateDraftPR(ctx, workdir, "master", branch, prTitle, buildPRBody(...))` call with `s.gh.CreatePR(ctx, workdir, "master", branch, prTitle, buildPRBody(...), s.prTarget)`.
   - Update the `NewExecutionStep` doc comment and the `prTitle` / step doc comments that say "draft PR" so they describe the configured target instead. Update the `glog.V(2).Infof("execution: draft PR opened …")` line to name the target.

7. **Add the parameter to the factory** in `pkg/factory/factory.go`:
   - `CreateAgent(..., claudeProber updatepkg.ClaudeProber, prTarget updatepkg.PRTarget) *agentlib.Agent` — `prTarget` last.
   - `CreateAgentProvider(..., claudeProber updatepkg.ClaudeProber, prTarget updatepkg.PRTarget) agentlib.AgentProvider` — `prTarget` last, forwarded to `CreateAgent`.
   - `CreateAgent` passes `prTarget` to `NewExecutionStep`.
   - Do NOT change `NewReviewStep` in this prompt — the review phase is the next prompt's scope.
   - Update the `CreateAgent` doc comment where it says `gh pr create --draft`.
   - The factory keeps zero business logic: no conditionals, no defaulting. Resolution of the empty value belongs to `ParsePRTarget` at startup.

8. **Add the setting to `main.go`**:
   - New field on `application`, placed next to `GhToken`:

     ```go
     // PRTarget selects how the agent opens pull requests: draft (default)
     // or ready. Unset behaves exactly as the previous release: drafts only.
     PRTarget string `required:"false" arg:"pr-target" env:"PR_TARGET" usage:"Pull request target at creation: draft (default) | ready"`
     ```

     Do NOT add a `default:"draft"` struct tag — the empty-value-to-draft resolution lives in `ParsePRTarget` (spec AC7 asserts the resolved target equals draft when the configuration value is the empty string).
   - At the very top of `application.Run`, before `registry := prometheus.NewRegistry()`, resolve and validate:

     ```go
     prTarget, err := updatepkg.ParsePRTarget(ctx, a.PRTarget)
     if err != nil {
         return err
     }
     ```

     Returning before the metrics registry is deliberate: an unrecognised value must stop the agent immediately, before any job accounting or credential work.
   - Pass `prTarget` as the new last argument to `factory.CreateAgentProvider`.

9. **Keep `application.Run` under the `funlen` limit (80 lines / 50 statements).** `Run` is currently ~80 lines, so requirement 8 would push it over. Extract the Kafka-deliverer block (everything from `deliverer := delivery.NewNoopResultDeliverer()` through the `factory.CreateKafkaResultDeliverer(...)` assignment) into a method on `application`:

   ```go
   // createResultDeliverer builds the Kafka-backed result deliverer when
   // TASK_ID is set, and the no-op deliverer otherwise. The returned close
   // func shuts down the sync producer (no-op when none was created).
   func (a *application) createResultDeliverer(ctx context.Context) (agentlib.ResultDeliverer, func(), error)
   ```

   - The no-op path returns `delivery.NewNoopResultDeliverer(), func() {}, nil`.
   - The Kafka path keeps the existing `KAFKA_BROKERS must be set when TASK_ID is set` error, the `errors.Wrap(ctx, err, "create sync producer")` wrap, and the `glog.Warningf("close sync producer failed: %v", err)` inside the returned close func.
   - In `Run`, call it once and record the failure metrics at the call site exactly as the other error branches do (`jobMetrics.RecordRun(agentlib.AgentStatusFailed)` + `jobMetrics.RecordDuration(time.Since(start))` + return the error), then `defer closeDeliverer()`.

10. **Add the setting to `cmd/run-task/main.go`** (the second entry point — it calls `factory.CreateAgent` directly and will not compile otherwise):
    - Same `PRTarget string` field with the same tags on that file's `application` struct.
    - In its `Run`, resolve with `updatepkg.ParsePRTarget(ctx, a.PRTarget)` before `factory.CreateAgent` and return the error unwrapped on failure.
    - Pass the resolved value as the new last argument to `factory.CreateAgent`.

11. **Regenerate the counterfeiter fake**: run `make generate`. Do NOT hand-edit `mocks/gh_cli.go`. After regeneration the fake exposes `CreatePRReturns`, `CreatePRCallCount`, `CreatePRArgsForCall` (returning seven values, the last being `pkg.PRTarget`).

12. **Update `pkg/steps_execution_test.go`**:
    - `pkg.NewExecutionStep(runner, ops, gh, gate, "tok", pkg.PRTargetDraft)` in the shared `BeforeEach`.
    - Rename every `CreateDraftPR*` fake call to `CreatePR*`.
    - The existing "opens the PR" assertion currently destructures six values from `CreateDraftPRArgsForCall(0)`; destructure seven and assert the seventh equals `pkg.PRTargetDraft`.
    - Add a new `Describe` that constructs the step with `pkg.PRTargetReady` and asserts the seam receives `pkg.PRTargetReady` — this proves the configured target reaches the seam rather than being re-derived.

13. **Add `pkg/gh_cli_test.go`** (`package pkg_test`, Ginkgo/Gomega) exercising the subprocess argv boundary via `pkg.PRCreateArgs`:
    - draft target → the returned argv contains `"--draft"` (spec AC1: the command the agent invoked contains the draft flag).
    - ready target → the returned argv does NOT contain `"--draft"` (spec AC2).
    - both targets → argv starts with `"pr", "create"` and carries `--base`/`--head`/`--title`/`--body` with the values passed in.

14. **Update `pkg/factory/factory_test.go`**: the two constructor call sites (`CreateAgentProvider` in the outer `BeforeEach`, `CreateAgent` in the `Describe("CreateAgent")` block) gain the new last argument. Use `updatepkg.PRTargetDraft` in the existing cases (import the pkg package with the same alias the factory uses).

15. Append one bullet to the existing `## Unreleased` section of `CHANGELOG.md`:
    `- feat: add PR_TARGET setting (draft | ready, default draft) selecting whether the agent opens draft or ready-for-review pull requests; GhCli.CreateDraftPR renamed to CreatePR and parameterized on the target`
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The default with no configuration present must be byte-identical in observable behaviour to the current release. This is the load-bearing constraint: an operator who upgrades and changes nothing must see no difference — `PR_TARGET` unset → `ParsePRTarget` → `PRTargetDraft` → `--draft` in the argv.
- The agent must not gain the ability to merge a pull request, or to change the draft-ness of a pull request that already exists. No new capability beyond choosing a target at creation time. Do NOT add any `gh pr ready` or `gh pr merge` invocation.
- Per-repository or per-task selection is out of scope. The setting is per-deployment; do not read a target from task frontmatter.
- The configuration must follow the same declarative mechanism the agent already uses for its other settings (`required` / `arg` / `env` / `usage` struct tags), so it appears in the generated usage output alongside them.
- The existing mock for the pull-request seam is generated, not hand-written. Regenerate it with `make generate`.
- Do NOT modify `pkg/steps_review.go`, `pkg/review_output.go` or `pkg/steps_review_test.go` — the review phase is the next prompt. `NewReviewStep` keeps its current four-parameter signature here.
- `checks.pr_draft` keeps its current meaning; do not touch `ReviewChecks`.
- Never `fmt.Errorf`; use `github.com/bborbe/errors`. Never `context.Background()` in non-test `pkg/` code.
- Do NOT run `go mod vendor` and do NOT use `-mod=vendor`; this repo does not commit `vendor/`.
- Existing tests must still pass; modified code paths must be covered by tests.
</constraints>

<verification>
Run `make test` — all tests pass.

Run `make precommit` — must exit 0 (it runs `generate`, so the regenerated fake is included).

```bash
grep -n 'PR_TARGET' main.go
# expect: at least one line (the setting reached the configuration struct)

grep -n 'PRTarget' pkg/gh_cli.go
# expect: at least one line (the creation seam takes the target)

grep -rn 'CreateDraftPR' --include='*.go' . | grep -v vendor
# expect: 0 lines — the draft-only name is gone everywhere, including mocks/

grep -rn 'pr ready\|pr merge' --include='*.go' . | grep -v vendor
# expect: 0 lines — no ready-flip or merge capability was added

grep -rn 'ParsePRTarget' main.go cmd/run-task/main.go
# expect: one line per entry point — both binaries resolve the setting

grep -n 'default:"draft"' main.go cmd/run-task/main.go
# expect: 0 lines — the empty-value default lives in ParsePRTarget, not a struct tag
```
</verification>
