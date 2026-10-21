---
status: completed
summary: 'Added a pushed-branch crash-window guard: RemoteBranchSHA on GitOps, a draft-PR adoption path in execution, tests, design doc and CHANGELOG entries'
execution_id: github-update-go-agent-adopt-branch-exec-020-execution-adopt-pushed-branch
dark-factory-version: v0.198.7
created: "2026-10-21T18:05:01Z"
queued: "2026-10-21T18:08:20Z"
started: "2026-10-21T18:09:08Z"
completed: "2026-10-21T18:15:39Z"
---

<summary>
- If an earlier run pushed the update branch and then crashed before opening the pull request, the re-run now opens a pull request from that pushed branch instead of redoing the update
- The re-run no longer gets stuck: before this, it redid the update, made a new commit, had its push rejected as non-fast-forward, and retried until the trigger cap, with no pull request ever opened
- A pull request opened this way is always a draft, because the agent cannot tell whether the first run was a salvage run that deliberately opened a draft
- On this path nothing is pushed, no new branch is created, and no Claude update or gate run happens; the agent only clones the pushed branch and opens the pull request
- If the branch-existence check itself fails, the agent logs a warning and continues exactly as before
- The existing open-PR adoption still wins when a PR is already open; the new check runs only when no open PR was found
- Vulnerability-fix runs use the same execution step, so they get the fix too
- A CHANGELOG bullet and the design doc's idempotency section describe the new guard
</summary>

<objective>
Close the crash window between `Push` and `CreatePR` in `executionStep.Run`. When the deterministic branch `fix/update-go-<ref:7>` already exists on the remote with no open PR, the step opens a draft PR from the pushed branch head and writes `## Result` outcome `opened`. It never re-pushes. Today the re-run makes a fresh commit with a new SHA, and its non-force push is rejected as `push_non_fast_forward` on every retry.
</objective>

<context>
Read CLAUDE.md (if present) and README.md for project conventions.
Read `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` and `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` (Ginkgo/Gomega + counterfeiter conventions).
Read `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` (`github.com/bborbe/errors` with ctx, never `fmt.Errorf`).
Read `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md`.

Files to read before changing anything:
- `pkg/git/git.go`: the `GitOps` interface, its per-method doc-comment style, and the `//counterfeiter:generate -o ../../mocks/git_ops.go --fake-name GitOps . GitOps` directive. The generate runner is `//go:generate go run -mod=mod github.com/maxbrunsfeld/counterfeiter/v6 -generate` in `pkg/git/git_suite_test.go`.
- `pkg/git/os_exec_git_ops.go`: `LsRemoteTags` and `ResolveDefaultBranchHead` are the exact pattern to follow: `exec.CommandContext(ctx, "git", "ls-remote", ...)`, `cmd.Env = g.cmdEnv()`, a stderr buffer, `errors.Errorf(ctx, "...: %s", redactToken(strings.TrimSpace(stderr.String())))`, a `// #nosec G204 -- ...` annotation, and a `glog.V(2).Infof` line.
- `pkg/git/os_exec_git_ops_test.go`: the `Describe("osExecGitOps boundary contracts")` block. Its `BeforeEach` builds a local `source` repo (branch `master`, two commits) and a `gitRun` helper. The `LsRemoteTags` / `ResolveDefaultBranchHead` `It`s call ops against the local `source` path. Follow that pattern for the new method's test.
- `pkg/steps_execution.go`:
  - `executionStep.Run` and its numbered doc comment (steps 1–10). The current order is: `replayGuard`, `extractFrontmatter`, `resolveUpdateScope`, `validatePlan`, `branch := branchPrefix + ref[:7]`, `adoptExistingPR`, `setupWorkdir` + deferred `os.RemoveAll`, `result := &ResultOutput{Branch: branch}`, `authedURL := injectToken(normalizeCloneURLToHTTPS(cloneURL), s.ghToken)`, `s.ops.ResolveDefaultBranchHead`, `CloneAtRef`, `SwitchNewBranch`, ...
  - `adoptExistingPR` (crash-window guard for an OPEN PR).
  - `commitPushAndOpenPR`: its tail (`s.gh.CreatePR(...)`, then `result.Outcome = ResultOutcomeOpened`, `result.PRURL = prURL`, `agentlib.MarshalSectionTyped(ctx, "## Result", *result)`, `md.ReplaceSection(section)`, return `Done` / `NextPhase: domain.TaskPhaseAIReview.String()`).
  - `fail` and `buildPRBody`.
- `pkg/gh_cli.go`: the `GhCli.CreatePR(ctx, workdir, base, head, title, body string, target PRTarget, label string) (string, error)` signature.
- `pkg/result_output.go`: `ResultOutput` and the `ResultOutcomeOpened` / `ResultOutcomeAdopted` constants.
- `pkg/git/error_classifier.go`: `ClassifyError` and `ErrorCategoryUnknown`. The `non-fast-forward` fragment maps to `ErrorCategoryPushNonFastForward`, which is the failure this prompt removes.
- `pkg/steps_execution_test.go`: the top-level `BeforeEach` (mocks wiring, `executionTaskMD` with `ref: 6d1f27fabcdef...`, so the branch is `fix/update-go-6d1f27f`) and `Describe("PR adopt guard (crash window)")`. New tests go next to that block.
- `docs/design.md` § 5.3 Idempotency, which describes the execution crash-window guard.
</context>

<requirements>
1. **Add `RemoteBranchSHA` to the `GitOps` interface** in `pkg/git/git.go`. Place it next to `LsRemoteTags` and give it a doc comment in the file's style:
   ```go
   // RemoteBranchSHA shells out `git ls-remote --heads <cloneURL> refs/heads/<branch>`
   // and returns the commit SHA the remote holds at that branch, or "" when the
   // branch does not exist on the remote. The execution step uses it to detect a
   // branch a prior run pushed before crashing ahead of PR creation.
   // cloneURL MUST already include any auth token.
   RemoteBranchSHA(ctx context.Context, cloneURL, branch string) (string, error)
   ```

2. **Implement `(g *osExecGitOps) RemoteBranchSHA`** in `pkg/git/os_exec_git_ops.go`, modelled on `LsRemoteTags`:
   - argv only: `exec.CommandContext(ctx, "git", "ls-remote", "--heads", cloneURL, "refs/heads/"+branch)`, with `cmd.Env = g.cmdEnv()` and a `// #nosec G204 -- cloneURL is authed by caller from validated frontmatter; branch is the deterministic fix/update-go-<sha:7> name` annotation.
   - On a non-zero exit, return `"", errors.Errorf(ctx, "git ls-remote --heads %s: %s", branch, redactToken(strings.TrimSpace(stderr.String())))`. Never include the raw `cloneURL` in the error, because it carries the token.
   - Parse stdout line by line (`strings.Fields`). Return `fields[0]` only for a line whose `fields[1]` equals exactly `"refs/heads/"+branch`. This guards against ls-remote's tail-matching of patterns. Empty output or no exact match returns `"", nil`.
   - Add `glog.V(2).Infof("git ls-remote --heads %s: sha=%q", branch, sha)`.

3. **Regenerate the counterfeiter mock** `mocks/git_ops.go` so it carries `RemoteBranchSHA`, `RemoteBranchSHAReturns`, `RemoteBranchSHACallCount`, and `RemoteBranchSHAArgsForCall`. Run `go generate -mod=mod ./pkg/git/...` from the repo root, or `make generate`. Never hand-edit the mock.

4. **Unit test for the implementation** in `pkg/git/os_exec_git_ops_test.go`, inside `Describe("osExecGitOps boundary contracts")`, following the `LsRemoteTags` `It`s:
   - Run `gitRun(source, "branch", "fix/update-go-abcdef0")`. `ops.RemoteBranchSHA(ctx, source, "fix/update-go-abcdef0")` must return `gitRun(source, "rev-parse", "fix/update-go-abcdef0")` with no error.
   - An absent branch (`"fix/update-go-0000000"`) must return `""` with no error.
   - A non-existent remote path (`filepath.Join(tmp, "missing")`) must return an error.
   - CloneAtRef contract for a non-default branch: after `gitRun(source, "branch", "fix/update-go-abcdef0")`, `ops.CloneAtRef(ctx, source, "fix/update-go-abcdef0", dest)` with `dest := filepath.Join(tmp, "cloned-pushed")` must succeed, and `gitRun(dest, "rev-parse", "HEAD")` must equal `gitRun(source, "rev-parse", "fix/update-go-abcdef0")`.

5. **New helper `openPRFromPushedBranch`** in `pkg/steps_execution.go`, placed after `adoptExistingPR`:
   ```go
   func (s *executionStep) openPRFromPushedBranch(
   	ctx context.Context,
   	md *agentlib.Markdown,
   	authedURL, workdir, branch string,
   	plan *PlanOutput,
   	result *ResultOutput,
   ) (*agentlib.Result, error)
   ```
   Behaviour, in order:
   - `s.ops.CloneAtRef(ctx, authedURL, branch, workdir)`, with ref = the branch name; checkout DWIM resolves it against origin. On error, return `s.fail(ctx, md, result, git.ClassifyError(err), err)`.
   - `s.gh.CreatePR(ctx, workdir, "master", branch, prTitle, buildPRBody(plan, &executionReport{}, nil), PRTargetDraft, s.autoMergeLabel)`. The target is always `PRTargetDraft`, never `s.prTarget`: the first run may have been a salvage run (forced draft), and that cannot be known here. On error, return `s.fail(ctx, md, result, git.ErrorCategoryUnknown, err)`, the same as `commitPushAndOpenPR`.
   - Set `result.Outcome = ResultOutcomeOpened` and `result.PRURL = prURL` (`result.Branch` is already set), marshal with `agentlib.MarshalSectionTyped(ctx, "## Result", *result)` (on error: `return nil, errors.Wrap(ctx, err, "marshal ## Result section")`), call `md.ReplaceSection(section)`, and return `&agentlib.Result{Status: agentlib.AgentStatusDone, NextPhase: domain.TaskPhaseAIReview.String()}, nil`.
   - Log `glog.V(2).Infof("execution: opened draft PR %s from already-pushed branch %s", prURL, branch)`.
   - This path NEVER calls `s.ops.Push`, `s.ops.SwitchNewBranch`, `s.ops.Commit`, `s.ops.ResolveDefaultBranchHead`, `s.bulk.Run`, `s.runner.Run`, or `s.gate.RunTarget`.
   - Doc comment: explain the crash window. A prior run pushed `fix/update-go-<ref:7>` and died before `gh pr create`. A re-run would make a new commit (Commit pins no dates, so the SHA changes) and its non-force push would be rejected as non-fast-forward until the trigger cap. The pushed head already passed the gates before it was pushed (the normal and salvage paths both push only after green gates), so opening the PR from it is safe. Say why the PR is always a draft.

6. **Wire the guard into `executionStep.Run`.** Insert it after `result := &ResultOutput{Branch: branch}` and `authedURL := ...`, and BEFORE the `s.ops.ResolveDefaultBranchHead` call:
   ```go
   pushedSHA, err := s.ops.RemoteBranchSHA(ctx, authedURL, branch)
   if err != nil {
   	glog.Warningf("execution: remote-branch lookup failed (continuing): %s", git.RedactToken(err.Error()))
   } else if pushedSHA != "" {
   	glog.V(2).Infof("execution: branch %s already pushed at %s with no open PR — opening PR from it", branch, pushedSHA)
   	return s.openPRFromPushedBranch(ctx, md, authedURL, workdir, branch, plan, result)
   }
   ```
   A lookup error falls through to the unchanged normal path. `adoptExistingPR` stays first: when an open PR exists, `RemoteBranchSHA` is never called.

7. **Update the numbered doc comment above `Run`.** After step 3 (PR-adopt guard), insert a step for the new guard: "Pushed-branch guard — `fix/update-go-<ref:7>` already on the remote with no open PR (a prior run crashed between push and PR create) → clone the pushed branch, open a DRAFT PR from it, write ## Result outcome=opened, re-route to ai_review; no update, no gate, no push. A lookup error falls through." Renumber the following steps so the list stays sequential.

8. **Check the PR-body wording on the empty report.** `buildPRBody(plan, &executionReport{}, nil)` emits the intro line, the `go directive` line when `plan.GoBump != nil`, the `gate green: <plan.GateTargets> (exit 0)` line, and the CHANGELOG handoff line. It omits the deps and vulns lines. That is honest: the gates ran green before the original push. Do NOT change `buildPRBody`.

9. **Ginkgo tests** in `pkg/steps_execution_test.go`. Add a new `Describe("pushed-branch guard (crash between push and PR create)")` directly after `Describe("PR adopt guard (crash window)")`, using the shared `BeforeEach` mocks. The mock default for `RemoteBranchSHA` is `"", nil`, so existing tests keep the normal path.
   - (a) `ops.RemoteBranchSHAReturns("feedface1234567890abcdef1234567890abcdef", nil)`. Then assert:
     - `result.Status == agentlib.AgentStatusDone` and `result.NextPhase == "ai_review"`.
     - `ops.PushCallCount() == 0`, `ops.SwitchNewBranchCallCount() == 0`, `ops.CommitCallCount() == 0`, `ops.ResolveDefaultBranchHeadCallCount() == 0`, `runner.RunCallCount() == 0`, `gate.RunTargetCallCount() == 0`, `bulk.RunCallCount() == 0`.
     - `ops.CloneAtRefCallCount() == 1`, with `CloneAtRefArgsForCall(0)` url `"https://x-access-token:tok@github.com/bborbe/demo.git"` and ref `"fix/update-go-6d1f27f"`.
     - `ops.RemoteBranchSHAArgsForCall(0)` returns that same authed url and branch `"fix/update-go-6d1f27f"`.
     - `gh.CreatePRCallCount() == 1`, with base `"master"`, head `"fix/update-go-6d1f27f"`, title `"update go module dependencies"`, target `pkg.PRTargetDraft`, and a body containing `gate green: precommit, check (exit 0)`.
     - `## Result` extracted via `agentlib.ExtractSection[pkg.ResultOutput]` has Outcome `pkg.ResultOutcomeOpened`, Branch `"fix/update-go-6d1f27f"`, and PRURL `"https://github.com/bborbe/demo/pull/42"`.
   - (a2) Same setup, but rebuild `step` with `pkg.PRTargetReady` (as `Describe("PRTargetReady passes ready target to the seam")` does). CreatePR must still receive `pkg.PRTargetDraft`.
   - (a3) `RemoteBranchSHA` returns a SHA and `gh.CreatePRReturns("", stderrors.New("gh boom"))`. The result is `agentlib.AgentStatusFailed`, `## Result` outcome is `pkg.ResultOutcomeFailed`, and `ops.PushCallCount() == 0`.
   - (b) `ops.RemoteBranchSHAReturns("", stderrors.New("ls-remote boom"))`. The normal path runs: `ops.SwitchNewBranchCallCount() == 1`, `ops.PushCallCount() == 1`, `gh.CreatePRCallCount() == 1`, and `result.Status == agentlib.AgentStatusDone`.
   - (c) In the existing `Describe("PR adopt guard (crash window)")` `It`, add `Expect(ops.RemoteBranchSHACallCount()).To(Equal(0))` so the adopt-first ordering is pinned.

10. **`docs/design.md` § 5.3 Idempotency**: append one sentence to the execution crash-window guard. When no open PR exists but `fix/update-go-<sha:7>` is already on the remote (crash between push and PR create), execution clones that branch and opens a draft PR from it instead of re-pushing; it covers vuln-fix runs too.

11. **`CHANGELOG.md`**: currently there is no `## Unreleased` section (the top section is `## v0.20.0`). Create `## Unreleased` directly above the newest `## vX.Y.Z` heading, with one blank line on each side. Never edit a released section. Add exactly this bullet:
    ```
    - fix: execution adopts an already-pushed `fix/update-go-<ref>` branch with no PR and opens a draft PR from it instead of re-pushing (lost claim between push and PR create; also vuln-fix runs)
    ```

12. **Self-check**: re-run the `<verification>` commands and confirm they pass. Walk each `<summary>` bullet against the diff.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must still pass
- Out of scope: the trigger retry cap, any claim/lease code, and `pkg/steps_fix_execution.go`. Do not modify them.
- The pushed-branch path NEVER pushes. Never add `--force` / `--force-with-lease` anywhere, and do not change `Push`.
- The PR opened from a pushed branch is always `PRTargetDraft`, regardless of the `PR_TARGET` deployment setting
- Do not add handling for a closed or merged PR on the pushed branch, and add no config knob or opt-out flag for the new guard
- `adoptExistingPR` stays first and unchanged; the new guard runs only when it returns nil
- Errors via `github.com/bborbe/errors` (`errors.Wrap` / `errors.Errorf` with ctx), never `fmt.Errorf`
- Every stderr or error string that can contain the authed URL goes through `redactToken` / `git.RedactToken`
- git shell-outs are argv only, never `sh -c`
- Never hand-edit `mocks/git_ops.go`; regenerate it
- Never touch `.github/workflows/`
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
Run `grep -c 'RemoteBranchSHA' mocks/git_ops.go` -- must print a number >= 1.
Run `grep -c 'openPRFromPushedBranch' pkg/steps_execution.go` -- must print a number >= 2.
Run `grep -c 'Describe("pushed-branch guard' pkg/steps_execution_test.go` -- must print 1.
Run `awk '/^## /{sec=$0} /already-pushed `fix\/update-go-<ref>`/{print "sits under: " sec}' CHANGELOG.md` -- must print `sits under: ## Unreleased`.
</verification>
