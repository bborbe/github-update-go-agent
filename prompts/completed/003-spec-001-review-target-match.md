---
status: completed
spec: [001-configurable-pr-target]
summary: ai_review now compares observed PR draft-ness against configured PRTarget instead of unconditionally requiring draft; checkPR returns bool match verdict, pr_draft in ReviewChecks unchanged (raw observed), mismatch note names both observed and configured state, factory forwards prTarget through production wiring to NewReviewStep, four pairing test cases added plus ViewPR error path and production-wiring factory test
execution_id: github-update-go-agent-pr-target-exec-003-spec-001-review-target-match
dark-factory-version: dev
created: "2026-08-15T09:10:00Z"
queued: "2026-08-15T08:16:38Z"
started: "2026-08-15T08:32:41Z"
completed: "2026-08-15T08:39:39Z"
branch: dark-factory/configurable-pr-target
---

# Review phase compares observed draft-ness against the configured target

<summary>
- The review phase stops demanding that every pull request be a draft.
- It now compares what it observes on the pull request against the target the deployment configured.
- A match passes: a draft pull request under a draft target, and a ready pull request under a ready target, both approve.
- A mismatch declines and records a note naming both what was observed and what was configured, so the operator sees why the task parked.
- The raw observed draft-ness keeps being reported unchanged in the review output, so downstream consumers that round-trip it see no change in meaning.
- The configured value reaches the review phase through the same production wiring the binary uses, not a test-only shortcut.
- All four combinations of observed and configured draft-ness are covered by tests.
- With no configuration present the behaviour is identical to the previous release: drafts approve, non-drafts decline.
- The review phase's other checks — pull request open, gates green, changelog, no new tag — are untouched.
- The agent still cannot ready an existing pull request and still cannot merge.
</summary>

<objective>
Make the `ai_review` phase treat "the pull request's draft-ness matches the configured `PRTarget`" as the passing condition, replacing its unconditional expectation of draft — and prove the configured value arrives through `factory.CreateAgent`, the production constructor, rather than a parallel test-only path.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read fully before changing anything:
- `pkg/pr_target.go` — the `PRTarget` type from prompt 1 (`PRTargetDraft`, `PRTargetReady`, `IsDraft()`, `String()`, `ParsePRTarget`). If this file does not exist, STOP: prompt 1 has not shipped.
- `pkg/steps_review.go` — `reviewStep`, `NewReviewStep`, `Run` (the `approved := …` expression), `checkPR`, `finish`, `notesFor`. This is the file this prompt changes.
- `pkg/review_output.go` — `ReviewChecks.PRDraft` and its `json:"pr_draft"` tag. Read for orientation; do NOT change it here (the doc-comment correction on line 13 belongs to the next prompt).
- `pkg/steps_review_test.go` — the existing Ginkgo suite: the shared `BeforeEach` happy-path fakes (`gh.ViewPRReturns("OPEN", true, nil)`, `ops.CloneAtRefStub` writing `changelogBranch`, `gate.RunTargetReturns("", 0, nil)`, `ops.RevListReturns`, `ops.LsRemoteTagsReturns`), the `reviewTaskMD` constant, and the `Describe("PR no longer draft")` block.
- `pkg/factory/factory.go` — `CreateAgent` (the `reviewStep := updatepkg.NewReviewStep(...)` line) and `CreateAgentProvider`. After prompt 2 both already take a trailing `prTarget updatepkg.PRTarget` parameter and forward it to `NewExecutionStep`; this prompt forwards the same value to `NewReviewStep` too.
- `pkg/factory/factory_test.go` — the existing `Describe("CreateAgent")` block and the `CreateAgentProvider` `BeforeEach`.
- `mocks/gh_cli.go`, `mocks/git_ops.go`, `mocks/gate_runner.go` — the counterfeiter fakes (`ViewPRReturns`, `CloneAtRefStub`, `ShowFileReturns`, `RevListReturns`, `LsRemoteTagsReturns`, `RunTargetReturns`).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-mocking-guide.md` — counterfeiter fakes are generated, never hand-edited.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-factory-pattern.md` — factories stay zero-logic, `Create*` prefix.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits, notably `funlen` (80 lines / 50 statements).
</context>

<requirements>
1. **Add the target to `reviewStep`** in `pkg/steps_review.go`:
   - New field `prTarget PRTarget` on the `reviewStep` struct, after `ghToken`.
   - New signature, `prTarget` last:

     ```go
     func NewReviewStep(
         ops git.GitOps,
         gh GhCli,
         gate GateRunner,
         ghToken string,
         prTarget PRTarget,
     ) agentlib.Step
     ```

   - Set the field in the returned struct literal.
   - Update the `NewReviewStep` doc comment to say the step also carries the configured pull-request target it compares observed draft-ness against.

2. **Make `checkPR` return the match verdict.** Change its signature to return a bool:

   ```go
   // checkPR verifies the PR is OPEN and that its observed draft-ness matches
   // the configured target. checks.PRDraft keeps reporting the RAW observed
   // draft-ness — the match decision lives in the returned verdict, the
   // approval flag and the notes, so the serialized pr_draft key does not
   // change meaning for downstream consumers. A mismatch means the PR is not
   // in the state the agent created it in (someone flipped it) — surfaced for
   // the operator, and per the check contract the review fails closed.
   func (s *reviewStep) checkPR(
     ctx context.Context,
     result *ResultOutput,
     checks *ReviewChecks,
     notes *[]string,
   ) bool
   ```

   Body, exactly:
   - On `s.gh.ViewPR` error: keep the existing `"gh pr view failed: "+err.Error()` note and `return false` (fail closed — an unknown state must never approve).
   - `checks.PROpen = state == "OPEN"` unchanged.
   - `checks.PRDraft = isDraft` unchanged — this is the RAW observed value, never the match verdict.
   - Keep the existing `if !checks.PROpen` note (`"pr state is "+state+", expected OPEN"`).
   - Compute `draftMatches := isDraft == s.prTarget.IsDraft()`.
   - Delete the old `if !checks.PRDraft { *notes = append(*notes, "pr is not a draft") }` block and replace it with:

     ```go
     if !draftMatches {
         *notes = append(*notes, "pr draft-ness mismatch: observed draft="+
             strconv.FormatBool(isDraft)+", configured target="+s.prTarget.String())
     }
     ```

     `strconv` is already imported by this file. Use exactly these two substrings — `observed draft=` and `configured target=` — the tests in requirement 6 assert on them.
   - `return draftMatches`.

3. **Use the verdict in `Run`**:
   - Replace `s.checkPR(ctx, result, &checks, &notes)` with `draftMatches := s.checkPR(ctx, result, &checks, &notes)`.
   - Replace the approval expression

     ```go
     approved := checks.PROpen && checks.PRDraft && checks.GateGreen &&
         checks.VulnsClear && checks.ChangelogUnreleased && checks.NoNewTag
     ```

     with

     ```go
     approved := checks.PROpen && draftMatches && checks.GateGreen &&
         checks.VulnsClear && checks.ChangelogUnreleased && checks.NoNewTag
     ```

     Every other conjunct keeps its current semantics.
   - Update step 2 of the `Run` doc comment (currently `2. pr_open + pr_draft — `gh pr view` must report OPEN + draft.`) so it says the pull request must be OPEN and its draft-ness must match the configured target.

4. **Correct the capability-claim comment in this file.** The `checkPR` doc comment currently reads "the agent never / readies" — one claim wrapped across lines 151-152, counted as a single site. The replacement in requirement 2 already removes it — verify the token `readies` no longer appears anywhere in `pkg/steps_review.go` (spec AC9 lists this as one of the eight sites). Do NOT edit `pkg/review_output.go`, `README.md` or `docs/design.md` here; the remaining sites belong to the next prompt.

5. **Forward the target in the factory** (`pkg/factory/factory.go`):
   - `CreateAgent` passes its `prTarget` parameter as the new last argument to `updatepkg.NewReviewStep(gitOps, ghCli, gateRunner, ghToken, prTarget)`.
   - Do NOT add a parameter, a conditional or a default — `CreateAgent` and `CreateAgentProvider` already carry `prTarget` from prompt 2, and the factory stays zero-logic.
   - Update the `CreateAgent` doc comment's `ai_review` bullet so it mentions that the PR-state check is target-aware.
   - Do NOT introduce any other construction path for `reviewStep`. The single-call-site lock in the verification section must keep returning exactly 1 line.

6. **Rework `pkg/steps_review_test.go`** so all four pairings are covered. Keep the existing suite structure and the shared happy-path fakes.
   - In the shared `BeforeEach`, construct with the default target: `step = pkg.NewReviewStep(ops, gh, gate, "tok", pkg.PRTargetDraft)`.
   - **Pairing draft/draft (approve)** — the existing `Describe("happy path")` already covers it. Add one assertion to its first `It`: `Expect(review.Notes).NotTo(ContainSubstring("draft-ness mismatch"))` (spec AC4: notes contain no draft-mismatch substring).
   - **Pairing ready/draft (decline)** — rename `Describe("PR no longer draft")` to something naming the mismatch (e.g. `Describe("target draft, PR is ready")`), keep `gh.ViewPRReturns("OPEN", false, nil)`, and assert: `result.Status == agentlib.AgentStatusFailed`, `review.Approved` is false, `review.Checks.PRDraft` is false, and `review.Notes` contains `"observed draft=false"` AND `"configured target=draft"`.
   - **Pairing ready/ready (approve)** — new `Describe`: nested `BeforeEach` sets `gh.ViewPRReturns("OPEN", false, nil)` and re-constructs `step = pkg.NewReviewStep(ops, gh, gate, "tok", pkg.PRTargetReady)`. Assert `result.Status == agentlib.AgentStatusDone`, `result.NextPhase == "human_review"`, `review.Approved` is true, and `review.Notes` does NOT contain `"draft-ness mismatch"`.
   - **Pairing draft/ready (decline)** — new `Describe`: nested `BeforeEach` keeps `gh.ViewPRReturns("OPEN", true, nil)` (the shared happy-path value) and re-constructs `step` with `pkg.PRTargetReady`. Assert `review.Approved` is false and `review.Notes` contains `"observed draft=true"` AND `"configured target=ready"`.
   - **`pr_draft` keeps raw semantics (spec AC10)** — inside the ready/ready `Describe`, one `It` asserting *both* `review.Checks.PRDraft` is **false** and `review.Approved` is **true** for the non-draft pull request under the ready target. This is the regression lock: `pr_draft` must never be rewritten to mean "matches target".
   - **ViewPR error path (new coverage)** — one `It`: `gh.ViewPRReturns("", false, stderrors.New("boom"))` → `result.Status == agentlib.AgentStatusFailed`, `review.Approved` is false, and `review.Notes` contains `"gh pr view failed"`. Requirement 2 adds a `return false` on this branch; the existing suite has no test for it (DoD: modified code paths must be covered).
   - Ginkgo ordering note: the outer `BeforeEach` runs before the nested one, so the mock instances created outside are the same ones the re-constructed step receives.

7. **Add the production-wiring test (spec AC6)** to `pkg/factory/factory_test.go`. It must drive `factory.CreateAgent` — the production constructor — not a new helper.
   - Add imports: `delivery "github.com/bborbe/agent/delivery"`, `domain "github.com/bborbe/vault-cli/pkg/domain"`, `"github.com/bborbe/github-update-go-agent/mocks"`, `updatepkg "github.com/bborbe/github-update-go-agent/pkg"`, plus `os` / `path/filepath` for the clone stub.
   - Add a package-level constant named `reviewTaskMD` to `pkg/factory/factory_test.go` carrying `phase: ai_review`, `repo`, `clone_url`, `ref`, a `## Plan` JSON block with `"gate_targets": ["precommit"]`, and a `## Result` JSON block with `branch` + `pr_url` — copy the shape of the same-named constant in `pkg/steps_review_test.go`. It has to be duplicated: `pkg` test fixtures are not importable from `factory_test`, and the two packages do not collide.
   - Declare a `ctx context.Context` in the new `Describe`'s own `BeforeEach` (`ctx = context.Background()`), the way the existing `Describe("CreateSyncProducer")` block does — the outer `CreateAgentProvider` block's `ctx` is scoped to that block.
   - New `Describe("CreateAgent with PRTargetReady approves a non-draft PR through the production wiring")`:

     ```go
     gh := &mocks.GhCli{}
     ops := &mocks.GitOps{}
     gate := &mocks.GateRunner{}
     gh.ViewPRReturns("OPEN", false, nil) // non-draft PR
     ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
         if err := os.MkdirAll(workdir, 0o750); err != nil {
             return err
         }
         return os.WriteFile(filepath.Join(workdir, "CHANGELOG.md"), []byte(changelogBranch), 0o600)
     }
     ops.ShowFileReturns([]byte(changelogMaster), nil)
     gate.RunTargetReturns("", 0, nil)
     ops.RevListReturns([]string{"deadbeef1", "deadbeef2"}, nil)
     ops.LsRemoteTagsReturns([]string{"1111111", "2222222"}, nil)

     agent := factory.CreateAgent(
         "", "", "", "tok", nil,
         ops, gh, gate,
         factory.CreateClaudeProber(""),
         updatepkg.PRTargetReady,
     )
     result, err := agent.Run(ctx, domain.TaskPhaseAIReview, reviewTaskMD, delivery.NewNoopResultDeliverer())
     ```

     Assert `err` is nil, `result.Status` equals `agentlib.AgentStatusDone` and `result.NextPhase` equals `"human_review"` — the non-draft pull request passed under the ready target, so the configured value reached the review phase through production wiring. Declare local `changelogBranch` / `changelogMaster` constants in this file with the same shapes used in `pkg/steps_review_test.go` (master has `## Unreleased` empty plus a `## v1.2.3` section; branch adds one bullet under `## Unreleased`).
   - Add the mirror case in the same `Describe`: the same fakes but `updatepkg.PRTargetDraft` → `result.Status` equals `agentlib.AgentStatusFailed` and `result.NextPhase` equals `""`. This proves the default still declines a non-draft pull request.
   - `agent.Run` walks only the requested phase here because `ai_review` returns `NextPhase: human_review`, which is a terminal literal for `agentlib.Agent.Run`.

8. **Update the existing `factory_test.go` call sites** if prompt 2 left them at a shorter argument list — every `factory.CreateAgent` / `factory.CreateAgentProvider` call must compile against the current signatures.

9. Append one bullet to the existing `## Unreleased` section of `CHANGELOG.md` (do not replace the section, do not add a version header):
   `- feat: ai_review compares the pull request's observed draft-ness against the configured PR_TARGET instead of requiring draft unconditionally; a mismatch declines with a note naming both the observed and the configured state, while checks.pr_draft keeps reporting raw observed draft-ness`
   Read `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for the entry style.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- `pr_draft` in the review output keeps its current meaning — raw observed draft-ness. The match-against-target decision lives in the approval flag and the notes, so the serialized key does not silently change meaning for downstream consumers that round-trip it into task frontmatter. Do NOT change `ReviewChecks` or any `json:` tag.
- The default with no configuration present must be byte-identical in observable behaviour to the current release: target draft + draft PR approves, target draft + non-draft PR declines with a note.
- The review phase's other checks — the pull request being open, the repository gate passing, the changelog entry, and the absence of a new tag — must keep their current semantics.
- The agent must not gain the ability to merge a pull request, or to change the draft-ness of a pull request that already exists. Do NOT add any `gh pr ready` or `gh pr merge` invocation, and do NOT make the review phase "fix" a mismatch.
- Do NOT introduce a second construction path for the review step (no test-only constructor, no `NewReviewStepWithTarget`). One call site, in `pkg/factory/factory.go`.
- Do NOT edit `pkg/review_output.go`, `README.md` or `docs/design.md` — the documentation sweep is the next prompt.
- The factory keeps zero business logic: no conditionals, no defaulting. Resolution of the empty value belongs to `ParsePRTarget` at startup.
- Per-repository or per-task selection is out of scope. The setting is per-deployment; do not read a target from task frontmatter.
- The existing mocks are generated, not hand-written. If a signature change requires a regenerated fake, run `make generate` — never hand-edit `mocks/`.
- Never `fmt.Errorf`; use `github.com/bborbe/errors`. Never `context.Background()` in non-test `pkg/` code.
- Do NOT run `go mod vendor` and do NOT use `-mod=vendor`; this repo does not commit `vendor/`.
- Keep functions inside the `funlen` limit (80 lines / 50 statements).
- Existing tests must still pass; modified code paths must be covered by tests.
</constraints>

<verification>
Run `make test` — all tests pass, including the four pairing cases and the factory-driven wiring cases.

Run `make precommit` — must exit 0.

```bash
grep -rn 'NewReviewStep(' --include='*.go' . | grep -v vendor | grep -v _test.go | grep -v 'func NewReviewStep'
# expect: exactly 1 line (pkg/factory/factory.go) — no parallel construction path (spec AC6)

grep -n 'prTarget' pkg/steps_review.go
# expect: the struct field, the constructor parameter and the checkPR comparison

grep -n 'readies' pkg/steps_review.go
# expect: 0 lines — the unconditional never-ready claim is gone from this file

grep -n 'pr is not a draft' pkg/steps_review.go
# expect: 0 lines — replaced by the mismatch note

grep -n 'checks.PRDraft = isDraft' pkg/steps_review.go
# expect: 1 line — pr_draft still reports the RAW observed value (spec AC10)

grep -rn 'pr ready\|pr merge' --include='*.go' . | grep -v vendor
# expect: 0 lines — no ready-flip or merge capability was added (spec AC8)

go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'steps_review|total'
# expect: checkPR and Run covered; total not lower than before
```
</verification>
