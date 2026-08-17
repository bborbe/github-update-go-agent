---
status: completed
spec: [002-bug-fabricated-vulnerability-advisories]
summary: 'Park-trust hardening complete: planning needs_input reasons claiming an environment problem (workdir/sandbox/permission/allowed paths/filesystem access) are stat-verified in Go before they can clear the assignee (refuted -> failed, standing -> needs_input unchanged), and the planning sub-call now runs through an in-repo claude runner that logs every tool_result body (token-redacted via git.RedactToken) at the deployed V(2) log level, mirroring the agent-lib subprocess env allowlist exactly; fixed a nil-vs-empty-slice contract bug in extractToolResultBodies and added coverage for Run/appendPlanningTail error and ring-buffer branches; make precommit exits 0'
execution_id: github-update-go-agent-fabricated-advisories-exec-006-spec-002-park-trust-hardening
dark-factory-version: dev
created: "2026-08-17T20:36:00Z"
queued: "2026-08-17T18:51:44Z"
started: "2026-08-17T19:28:22Z"
completed: "2026-08-17T19:37:34Z"
---

# Park-trust hardening: verify environment claims in Go and capture tool_result bodies in the planning log

<summary>
- A `needs_input` reason that claims an environment problem (workdir / sandbox / permission) is no longer trusted as authoritative: the agent `stat`s its own workdir in Go before the reason can clear the assignee.
- When the workdir exists on disk, the claim is refuted and the run fails with a message naming the claim and the stat result — the assignee is not cleared on a false claim.
- When the workdir genuinely does not exist, the claim stands and the existing `needs_input` path is unchanged.
- The planning sub-call now runs through an in-repo Claude runner that logs every `tool_result` body at the deployed log level (`-v=2`), so a future "blocked" / "cannot access" claim can be confirmed or refuted from the pod log alone.
- The tool_result capture redacts URL token material through the existing token-redaction helper before anything is written to the log.
- The in-repo runner mirrors the agent-lib subprocess construction exactly (same CLI argv, same environment allowlist) so no pod secret leaks into the Claude subprocess.
- The shared agent-lib runner stays in use for the execution and healthcheck phases — only the planning sub-call gets the capturing runner.
- Unit tests drive the refute/stand decision and assert the tool_result body is captured verbatim by the log sink.
</summary>

<objective>
Close the two remaining trust surfaces that the same root cause produced: a `needs_input` reason claiming an environment problem (workdir/sandbox/permission) must be stat-verified in Go before it can clear the assignee, and the raw `tool_result` bodies of the planning sub-call must be written at the deployed log level so a future environment claim is confirmable from the log alone.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read before changing anything:
- `docs/design.md` — `## 7.1 Error handling` (the agent emits `Status: failed`/`needs_input`; the CONTROLLER owns the envelope and clears the assignee) and `## Suppression surfaces`.
- `pkg/steps_planning.go` — the `Run` routing after prompt 1: `runInspection` returns `(plan, table, failResult)`, and the `plan.Outcome == PlanOutcomeNeedsInput` branch currently writes the plan section and returns `needsInput(plan.Reason)`. You will add the env-claim refutation just before that branch.
- `pkg/steps_gh_token.go` — `needsInput(msg)` and `failed(msg)` result shapes.
- `pkg/export_test.go` — the in-repo test-export pattern (`package pkg` internal test file exposing unexported identifiers, e.g. `ParseLLMJSONProbe`).
- `pkg/factory/factory.go` — `CreateAgent` builds `planningRunner := CreateClaudeRunner(claudeConfigDir, agentDir, planningTools, model, claudeEnv)` and passes it to `updatepkg.NewPlanningStep`. You will swap the planning runner construction.
- `pkg/git/os_exec_git_ops.go` — `RedactToken(s string) string` (the existing token redaction on URL material, `x-access-token:...@`).
- `pkg/steps_planning_test.go` — the prompt-1 fixture-workdir shape (`ops.CloneAtRefStub` creates the fixture workdir; the step is constructed with the real `updatepkg.NewOSExecGateRunner()`).
- The agent-lib Claude runner source, which your in-repo runner must mirror EXACTLY for the CLI argv and the subprocess env allowlist. Read them at:
  - `$(go env GOMODCACHE)/github.com/bborbe/agent@v0.79.0/claude/claude-runner.go` — `buildCommand` (argv `--print --output-format stream-json --verbose --strict-mcp-config`, `--allowedTools`, `--model`, `WorkingDirectory`, `cmd.Stdin = bytes.NewBufferString(prompt)`) and `buildSubprocessEnv` (the allowlist `{HOME, PATH, USER, TZ, ZONEINFO, TMPDIR, LANG, LC_ALL}`, then `CLAUDE_CONFIG_DIR` precedence config > env > `~/.claude`, then `r.config.Env` overrides).
  - `$(go env GOMODCACHE)/github.com/bborbe/agent@v0.79.0/claude/claude-runner-config.go` — `ClaudeRunnerConfig` field names and types.
  - `$(go env GOMODCACHE)/github.com/bborbe/agent@v0.79.0/claude/claude-event.go` — the stream-json event shapes (`type: result` with `result` text; `message.content[]` items; `content_block_delta` events).
  - `$(go env GOMODCACHE)/github.com/bborbe/agent@v0.79.0/claude/claude-result.go` — `ClaudeResult{Result string}`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-glog-guide.md` — glog verbosity semantics: the deployed image entrypoint runs `/main -v=2`, so V(2) IS the deployed log level; debug-shaped content goes through `glog.V(N)`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors`; never `fmt.Errorf`, never `context.Background()` in `pkg/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage.
- `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — changelog entry style.
- `/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md` — new code needs ≥80% statement coverage.

Run this first to confirm the current planning runner construction and the needs_input routing:

```bash
grep -n 'CreateClaudeRunner\|planningRunner\|NewPlanningStep' pkg/factory/factory.go
grep -n 'PlanOutcomeNeedsInput' pkg/steps_planning.go
```
</context>

<requirements>
1. **Add the environment-claim refutation helpers** in `pkg/steps_planning.go` (near `parkMessage`):

   ```go
   // environmentClaimMarkers are needs_input reason substrings that claim an
   // environment problem (workdir/sandbox/permission) rather than a real repo
   // finding. Such a claim is stat-verified before it may clear the assignee.
   var environmentClaimMarkers = []string{
       "workdir",
       "sandbox",
       "permission",
       "allowed paths",
       "filesystem access",
   }

   // claimsEnvironmentProblem reports whether reason reads as an environment
   // claim (case-insensitive marker match).
   func claimsEnvironmentProblem(reason string) bool

   // refuteEnvironmentClaim stat-checks the agent's own workdir against an
   // environment-claim needs_input reason. The planning step cloned into
   // workdir, so an existing workdir refutes a "cannot access workdir /
   // sandbox" claim. Logs the stat result alongside the claim at V(2) so the
   // evidence is visible in the deployed pod log.
   func refuteEnvironmentClaim(workdir string, reason string) bool
   ```

   Behaviour:
   - `claimsEnvironmentProblem`: `strings.ToLower(reason)` and report true when any marker is a substring of the lowered reason.
   - `refuteEnvironmentClaim`: if `!claimsEnvironmentProblem(reason)` return false. Otherwise `os.Stat(workdir)`; `exists := err == nil`; log `glog.V(2).Infof("planning: env-claim check workdir=%s stat_exists=%t reason=%q", workdir, exists, reason)`; return `exists`.

2. **Wire the refutation into `Run`** in `pkg/steps_planning.go`. In the `plan.Outcome == PlanOutcomeNeedsInput` branch (before `writePlanSection`/`needsInput`), add:

   ```go
   if plan.Outcome == PlanOutcomeNeedsInput {
       if refuteEnvironmentClaim(workdir, plan.Reason) {
           glog.V(2).Infof("planning: env-claim refuted — workdir exists, not clearing assignee: reason=%q", plan.Reason)
           return failed("needs_input reason claims an environment problem but workdir " + workdir + " exists on disk — false claim, not clearing assignee: " + plan.Reason), nil
       }
       if err := writePlanSection(ctx, md, plan); err != nil {
           return nil, err
       }
       return needsInput(plan.Reason), nil
   }
   ```

   Semantics: when the workdir exists the claim is refuted → `failed` (NOT `needs_input`), so the controller does not clear the assignee; the message names the claim and the stat result for the operator. When the workdir genuinely does not exist, `refuteEnvironmentClaim` returns false and the existing `needs_input` path runs unchanged.

3. **Create `pkg/planning_runner.go`** in `package pkg` with the 3-line BSD copyright header copied from `pkg/review_output.go`. It implements an in-repo `claudelib.ClaudeRunner` for the planning sub-call that logs every `tool_result` body at the deployed log level:

   ```go
   // planningRunner is an in-repo claudelib.ClaudeRunner for the planning
   // sub-call. The agent-lib runner (claudelib.NewClaudeRunner) does not log
   // tool_result bodies at the deployed log level (-v=2), so a model claim
   // like "cannot access workdir — blocked by sandbox" could previously not be
   // confirmed or refuted from the pod log. This runner mirrors the agent-lib
   // subprocess construction (same CLI argv, same env allowlist) and
   // additionally logs every tool_result content body at glog V(2),
   // token-redacted via git.RedactToken.
   type planningRunner struct {
       config  claudelib.ClaudeRunnerConfig
       logSink func(context.Context, string)
   }

   // NewPlanningRunner constructs the tool-result-logging planning runner.
   func NewPlanningRunner(config claudelib.ClaudeRunnerConfig) claudelib.ClaudeRunner {
       return &planningRunner{config: config, logSink: defaultLogPlanningToolResult}
   }
   ```

4. **Implement `Run` on `planningRunner`** mirroring the agent-lib flow (`claude-runner.go` `claudeRunner.Run`), with the tool_result logging added:

   ```go
   func (r *planningRunner) Run(ctx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
       cmd, err := r.buildCommand(ctx, prompt)
       if err != nil {
           return nil, errors.Wrap(ctx, err, "build command")
       }
       stdoutPipe, err := cmd.StdoutPipe()
       if err != nil {
           return nil, errors.Wrap(ctx, err, "create stdout pipe")
       }
       if err := cmd.Start(); err != nil {
           return nil, errors.Wrap(ctx, err, "start claude CLI")
       }
       resultText, tail := scanPlanningOutput(ctx, stdoutPipe)
       if err := cmd.Wait(); err != nil {
           var tailMsg string
           if len(tail) > 0 {
               tailMsg = strings.Join(tail, " | ")
           } else {
               tailMsg = "no stdout captured"
           }
           return nil, errors.Wrapf(ctx, err, "claude CLI failed: %s", tailMsg)
       }
       if resultText == "" {
           return nil, errors.New(ctx, "no result event found in claude CLI output")
       }
       return &claudelib.ClaudeResult{Result: resultText}, nil
   }
   ```

   Imports: `bufio`, `bytes`, `context`, `encoding/json`, `io`, `os`, `os/exec`, `strings`, `github.com/bborbe/errors`, `github.com/golang/glog`, `claudelib "github.com/bborbe/agent/claude"`, and `"github.com/bborbe/github-update-go-agent/pkg/git"`. No new module dependencies.

5. **Implement `buildCommand` and `buildSubprocessEnv`** on `planningRunner`, mirroring the agent-lib `claude-runner.go` implementations EXACTLY (this is the security-relevant boundary — the subprocess env allowlist must be replicated verbatim so no pod secret leaks into the Claude CLI):

   ```go
   func (r *planningRunner) buildCommand(ctx context.Context, prompt string) (*exec.Cmd, error) {
       args := []string{"--print", "--output-format", "stream-json", "--verbose", "--strict-mcp-config"}
       if len(r.config.AllowedTools) > 0 {
           args = append(args, "--allowedTools", r.config.AllowedTools.String())
       }
       if r.config.Model != "" {
           args = append(args, "--model", r.config.Model.String())
       }
       // #nosec G204 -- fixed argv (production sets name=claude); no task input reaches the command line
       cmd := exec.CommandContext(ctx, "claude", args...)
       if r.config.WorkingDirectory != "" {
           workDir, err := r.config.WorkingDirectory.Resolve(ctx)
           if err != nil {
               return nil, errors.Wrap(ctx, err, "resolve WorkingDirectory")
           }
           cmd.Dir = workDir
       }
       cmd.Stdin = bytes.NewBufferString(prompt)
       env, err := r.buildSubprocessEnv(ctx)
       if err != nil {
           return nil, errors.Wrap(ctx, err, "build subprocess env")
       }
       cmd.Env = env
       return cmd, nil
   }
   ```

   `buildSubprocessEnv` must apply, in order: (a) the allowlist pass-through for exactly `HOME`, `PATH`, `USER`, `TZ`, `ZONEINFO`, `TMPDIR`, `LANG`, `LC_ALL`; (b) `CLAUDE_CONFIG_DIR` with precedence `r.config.ClaudeConfigDir` > `os.Getenv("CLAUDE_CONFIG_DIR")` > default `"~/.claude"`, resolved via `ClaudeConfigDir.Resolve(ctx)`; (c) `r.config.Env` overrides last; (d) result as `[]string` of `k+"="+v`. Read the agent-lib `buildSubprocessEnv` in `<context>` and replicate it line-for-line — do not add or remove allowlist keys.

6. **Implement the stream scanner** in `pkg/planning_runner.go`:

   ```go
   // scanPlanningOutput reads stream-json lines, logs every tool_result body
   // at V(2), and returns the last non-empty result event text plus a bounded
   // tail of all non-empty lines for failure messages. The log sink is injected
   // so tests can capture the written bodies verbatim.
   func scanPlanningOutput(ctx context.Context, reader io.Reader, logSink func(context.Context, string)) (string, []string)

   // extractToolResultBodies returns the raw text bodies of every tool_result
   // content block in one stream-json line. Claude Code emits tool_result as a
   // message.content item (never as a content_block_delta — the stream carries
   // text_delta/input_json_delta/thinking_delta, not tool_result). The body
   // lives in a nested content[].text, or directly in a string content field.
   // Returns nil for lines without a tool_result.
   func extractToolResultBodies(line []byte) []string

   // planningResultText returns the result event's text for a stream-json line
   // of type "result" with a non-empty result, and false otherwise.
   func planningResultText(line []byte) (string, bool)
   ```

   `scanPlanningOutput`: a `bufio.Scanner` with the same buffer sizing as the agent-lib (`scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)`); honours `ctx.Done()`; per line: append to a bounded tail (reuse the agent-lib ring-buffer behaviour: keep the last 5 non-empty lines, each capped at 512 bytes); for each body from `extractToolResultBodies(line)` call `logSink(ctx, git.RedactToken(body))`; and on `planningResultText(line)` return the text. Use a `strings.Builder` or slice for the tail; the tail is only surfaced on subprocess failure, mirroring the agent-lib `tailMaxLines = 5` / `tailMaxBytes = 512`.

   For `extractToolResultBodies`, define a small block type and unmarshal with `encoding/json`:

   ```go
   type toolResultBlock struct {
       Type    string          `json:"type"`
       Text    string          `json:"text"`
       Content json.RawMessage `json:"content"`
   }
   ```

   Parse each line into a struct with `Message.Content []toolResultBlock` (no delta branch — Claude Code does not emit tool_result as a content_block_delta). Collect, in order, for every `message.content` item with `Type == "tool_result"`: the direct `Text` if non-empty, then the `Content` field — it may be a JSON string (take it verbatim) or an array of blocks (take each `Type == "text"` item's `Text` if non-empty). Malformed JSON → nil. A string-content variant must NOT silently drop the body (that is the exact evidence this capture exists for).

7. **Define the default log sink** in `pkg/planning_runner.go`, held on the struct (constructor-injected, per the no-test-only-package-mutable-state rule):

   ```go
   // defaultLogPlanningToolResult writes one redacted tool_result body at the
   // deployed log level (V(2) — the image entrypoint runs /main -v=2).
   // Installed as planningRunner.logSink by NewPlanningRunner; tests inject
   // their own capture func via the constructor instead of swapping package state.
   func defaultLogPlanningToolResult(ctx context.Context, body string) {
       glog.V(2).Infof("planning: tool_result %s", body)
   }
   ```

   Do NOT put raw token material here — every body passes through `git.RedactToken` before reaching this sink (spec Security row: "No token leakage in new logging").

8. **Add test-only exports** in `pkg/export_test.go` (same `package pkg` pattern as the existing `ParseLLMJSONProbe` exports):

   ```go
   var (
       ExtractToolResultBodies  = extractToolResultBodies
       PlanningResultText       = planningResultText
       ScanPlanningOutput       = scanPlanningOutput
       RefuteEnvironmentClaim   = refuteEnvironmentClaim
       PlanningRunnerForTest    = func(config claudelib.ClaudeRunnerConfig, sink func(context.Context, string)) *planningRunner { return &planningRunner{config: config, logSink: sink} }
       PlanningRunnerBuildCmd   = func(r *planningRunner, ctx context.Context, prompt string) (*exec.Cmd, error) { return r.buildCommand(ctx, prompt) }
   )
   ```

   No `SetLogPlanningToolResultForTest` — the sink is constructor-injected via `PlanningRunnerForTest(config, captureFunc)` (no test-only package-level mutable state).

   This requires adding `os/exec` and `claudelib "github.com/bborbe/agent/claude"` imports to `pkg/export_test.go` (the file currently imports only `context`).

9. **Wire the planning runner in `pkg/factory/factory.go`.** Replace the planning runner construction inside `CreateAgent`:

   ```go
   planningRunner := updatepkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{
       ClaudeConfigDir:  claudeConfigDir,
       AllowedTools:     planningTools,
       Model:            model,
       WorkingDirectory: agentDir,
       Env:              claudeEnv,
   })
   ```

   Keep `CreateClaudeRunner` (the agent-lib wrapper) for the execution and healthcheck runners — only the planning sub-call gets the capturing runner. `CreateAgent`'s signature is unchanged.

10. **Create `pkg/planning_runner_test.go`** in `package pkg_test` covering:
    - `extractToolResultBodies` verbatim capture from a realistic `message.content` tool_result line — build the fixture line as the claude stream-json emits it, e.g. `{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"content":[{"type":"text","text":"GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6"}]}]}}` — and assert the returned body equals the inner text exactly.
    - **String-content variant**: a tool_result whose `content` is a plain JSON string (not an array), e.g. `{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"content":"make: cannot access workdir /tmp/github-update-go-x"}]}}` — assert the string body is captured verbatim (this is the "blocked"-claim evidence shape; a string-content body must NOT be silently dropped).
    - `extractToolResultBodies` returns nil for a tool_use line, a result line, and malformed JSON.
    - `planningResultText`: `{"type":"result","result":"{\"outcome\":\"ready\"}"}` → the inner text, true; a non-result or empty-result line → false.
    - Log-sink verbatim capture (spec AC8 unit-test branch): `captured := []string{}`; `sink := func(_ context.Context, body string) { captured = append(captured, body) }`; feed a multi-line stream fixture containing a result line and a tool_result line through `pkg.ScanPlanningOutput(ctx, reader, sink)`; assert the captured slice contains the tool_result body verbatim and the returned result text is correct.
    - Token redaction at the sink boundary: a tool_result body containing `https://x-access-token:ghs_secret123@github.com/o/r.git` is captured (after `ScanPlanningOutput` applies `git.RedactToken`) as `https://x-access-token:[REDACTED]@github.com/o/r.git` — never the raw token.
    - Subprocess env boundary: `t.Setenv`/Ginkgo-equivalent set `SOME_POD_SECRET=xyz` and `CLAUDE_CONFIG_DIR` to a temp dir; `pkg.PlanningRunnerBuildCmd(pkg.PlanningRunnerForTest(config), ctx, "prompt")` → assert the resulting `*exec.Cmd` argv contains `--print`, `--output-format`, `stream-json`, `--model` (when config sets it), `cmd.Dir` resolves the working directory, `cmd.Env` does NOT contain `SOME_POD_SECRET` (allowlist boundary — no pod secret leaks), contains `CLAUDE_CONFIG_DIR`, and contains a `GH_TOKEN` override when `config.Env` sets one.

11. **Add the env-claim tests** to `pkg/steps_planning_test.go` (using the prompt-1 fixture-workdir shape with the real `updatepkg.NewOSExecGateRunner()`):
    - **Refuted claim (AC7, workdir exists)**: fixture Makefile with a `check` target that echoes one clean finding row; `ops.CloneAtRefStub` creates the workdir (so it exists on disk); runner returns `{"outcome":"needs_input","has_work":false,"reason":"cannot access workdir /tmp/github-update-go-test-task-1 — all filesystem access is blocked by sandbox restrictions"}`. Assert `AgentStatusFailed` (assignee NOT cleared — not `needs_input`), and the message contains the workdir path and the claim substring.
    - **Standing claim (AC7, workdir absent, helper level)**: this branch is unreachable through `Run` — prompt 1's `runInspection` returns `needsInput("no gate target found...")` before calling the model when the workdir/Makefile is absent (no gate target can run against a missing workdir). Cover it at the helper level: `pkg.RefuteEnvironmentClaim("<non-existent-path>", "<env-claim reason>")` returns false (claim stands when the workdir does not exist), and `pkg.RefuteEnvironmentClaim("<existing-workdir>", "no fixed version available")` returns false for a non-environment reason regardless of the path. Put these two cases in `pkg/planning_runner_test.go` or `pkg/export_test.go`-adjacent unit tests.
    - **Non-environment needs_input unchanged**: runner returns `{"outcome":"needs_input","has_work":false,"reason":"no fixed version available"}` with the workdir existing. Assert `AgentStatusNeedsInput` with the reason (no refutation for a non-environment claim).
    - **False sandbox claim variant**: runner returns `{"outcome":"needs_input","has_work":false,"reason":"directory not in allowed paths (/agent only)"}` with the workdir existing. Assert `AgentStatusFailed` (the `allowed paths` marker is refuted by the existing workdir).

12. **Ensure a `## Unreleased` section exists** at the top of `CHANGELOG.md` (prompt 1 creates it first; if it is absent, create the `## Unreleased` header per the changelog guide) and append one bullet to it (do not replace the section, do not add a version header):

    `- fix: planning refutes false workdir/sandbox/permission needs_input claims with a Go stat before they can clear the assignee, and logs planning sub-call tool_result bodies (token-redacted) at the deployed log level`
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- The agent must remain incapable of writing ignore entries; parking stays the only agent-side move on an unfixable finding (design D4, `suppressionSurfacesHint`).
- No token leakage in the new logging: every tool_result body passes through `git.RedactToken` (the existing `x-access-token:...@` URL-material redaction) before it reaches the log sink. Do not log raw prompt or config content beyond the tool_result bodies.
- The in-repo runner's subprocess env allowlist must be replicated EXACTLY from the agent-lib `buildSubprocessEnv` — do not add or remove allowlist keys; `r.config.Env` overrides are the only way additional vars (e.g. `GH_TOKEN`) cross into the Claude CLI.
- Only the planning sub-call uses the new runner. The execution and healthcheck runners keep using `claudelib.NewClaudeRunner` via `CreateClaudeRunner` — do not change them.
- `## Plan` output contract (`PlanOutput`) and the `## Plan` section format are unchanged. The needs_input message content for the standing-claim case must be byte-identical to today.
- Error handling follows the repo's `github.com/bborbe/errors` convention; no `fmt.Errorf`, no `context.Background()` in `pkg/` non-test code.
- No new module dependencies.
- Do NOT change `main.go`, `cmd/run-task/main.go`, or the `pkg/factory/factory.go` `CreateClaudeRunner` function — the factory change is scoped to the planning runner construction inside `CreateAgent`.
- Existing tests must still pass; new code needs ≥80% statement coverage (`/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`).
- This prompt assumes prompt 1 has been applied (the planning path now detects/runs/parses the gate in Go). Do not re-implement or revert any of prompt 1's changes.
</constraints>

<verification>
Run `make test` — all tests pass, including the new `pkg/planning_runner_test.go` and the env-claim cases in `pkg/steps_planning_test.go`.

Run `make precommit` — must exit 0.

```bash
grep -n 'tool_result' pkg/planning_runner.go
# expect: the extraction, the scan loop, and the log sink write lines

grep -n 'env-claim check' pkg/steps_planning.go
# expect: >= 1 line — the stat result is logged alongside the claim

grep -rn 'NewPlanningRunner' pkg/factory/factory.go pkg/planning_runner.go
# expect: the constructor definition and the CreateAgent wiring

grep -rn 'RedactToken' pkg/planning_runner.go
# expect: the tool_result body is redacted before the sink

go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'planning_runner|steps_planning|total'
# expect: planning_runner functions at or near 100%; the env-claim helpers covered
```
</verification>
