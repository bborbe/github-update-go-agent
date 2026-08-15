---
status: completed
spec: [001-configurable-pr-target]
summary: Added PRTarget value type (draft|ready) with ParsePRTarget, Validate, IsDraft, String methods and full test coverage in pkg/pr_target.go and pkg/pr_target_test.go
execution_id: github-update-go-agent-pr-target-exec-001-spec-001-pr-target-type
dark-factory-version: dev
created: "2026-08-15T01:30:00Z"
queued: "2026-08-15T08:16:37Z"
started: "2026-08-15T08:21:51Z"
completed: "2026-08-15T08:25:29Z"
branch: dark-factory/configurable-pr-target
---

# Add a PRTarget value type with parsing, validation and default-to-draft

<summary>
- Introduces a typed value for "how should the agent open a pull request": draft or ready.
- An empty or unset value resolves to draft, so an operator who upgrades and changes nothing sees no difference.
- Any other value is rejected with an error that names the offending value and both accepted values.
- Rejection happens at parse time, so a misconfigured deployment can fail fast instead of silently falling back.
- The value knows whether it means "draft", which later lets the PR-creation code decide whether to pass the draft flag.
- Nothing is wired up yet — this prompt only establishes the value type and its tests.
- No behaviour of the running agent changes in this prompt.
- No new module dependencies are added.
</summary>

<objective>
Establish a `PRTarget` value type (`draft` | `ready`) with parsing, validation and default-to-draft resolution in `pkg/`, so that later prompts can thread one configured value into both the PR-creation seam and the review phase. This is the foundation that makes the deployment-level choice type-safe and makes an unrecognised configuration value fail loudly at startup instead of being silently coerced.
</objective>

<context>
Read `CLAUDE.md` (repo root) for project conventions.

Read before changing anything:
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-enum-type-pattern.md` — the canonical string-enum shape (typed constants, `Available*` collection, `String()`, `Validate(ctx)`, plural type with `Contains`).
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — `github.com/bborbe/errors` usage; never `fmt.Errorf`, never `context.Background()` in `pkg/`.
- `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega, external `_test` package, coverage.
- `pkg/git/error_classifier.go` — the in-repo precedent for a closed string enum (`ErrorCategory`) including the doc-comment style.
- `pkg/review_output.go` — license header + package/doc-comment style for a small `pkg` file.
- `pkg/url_helpers_test.go` and `pkg/pkg_suite_test.go` — the test style: `package pkg_test`, Ginkgo `Describe`/`It`, Gomega matchers, no `testing.T` assertions.
- `pkg/gh_cli.go` — the consumer that will take this type in the next prompt (read for orientation only; do NOT change it here).
</context>

<requirements>
1. Create `pkg/pr_target.go` in `package pkg`. Start the file with the exact 3-line BSD copyright header used by every other file in `pkg/` (copy from `pkg/review_output.go`).

2. Declare the enum following `go-enum-type-pattern.md`:

   ```go
   // PRTarget is the closed set of pull-request targets the agent may open a
   // pull request as. It is chosen per deployment at creation time only — the
   // agent never flips an already-open pull request and never merges.
   type PRTarget string

   const (
       // PRTargetDraft opens the pull request as a draft (the default).
       PRTargetDraft PRTarget = "draft"
       // PRTargetReady opens the pull request ready for review.
       PRTargetReady PRTarget = "ready"
   )

   // PRTargets is a collection of PRTarget values.
   type PRTargets []PRTarget

   // AvailablePRTargets lists every PRTarget the agent accepts. Validate
   // ranges over this collection — it is the single source of truth.
   var AvailablePRTargets = PRTargets{PRTargetDraft, PRTargetReady}
   ```

3. Implement `func (p PRTargets) Contains(target PRTarget) bool` with a plain `for … range` loop. Do NOT import `github.com/bborbe/collection` or `github.com/bborbe/validation` — both are currently indirect dependencies in `go.mod` and this prompt must not promote them to direct dependencies.

4. Implement on the singular type:
   - `func (p PRTarget) String() string` — returns `string(p)`.
   - `func (p PRTarget) IsDraft() bool` — returns `p == PRTargetDraft`. This is the predicate the PR-creation seam will use to decide whether to pass the draft flag, and the review phase will use to compare against observed draft-ness.
   - `func (p PRTarget) Validate(ctx context.Context) error` — returns `nil` when `AvailablePRTargets.Contains(p)`, otherwise the error described in requirement 6. Do NOT implement this as an inline `switch`.

5. Implement the parse entry point:

   ```go
   // ParsePRTarget resolves the configured pull-request target. An empty
   // value means "unset" and resolves to PRTargetDraft, preserving the
   // pre-configuration behaviour byte-for-byte. Any other unrecognised value
   // is rejected.
   func ParsePRTarget(ctx context.Context, value string) (PRTarget, error)
   ```

   Behaviour, exactly:
   - `value == ""` → `PRTargetDraft, nil`.
   - otherwise construct `PRTarget(value)` and call `Validate(ctx)`; on error return `PRTarget(""), err` (wrapped or returned as-is, but the message must still satisfy requirement 6).
   - on success return the constructed value.
   - Do NOT trim whitespace and do NOT case-fold. `"Draft"`, `"DRAFT"`, `" draft "` are all rejected — the configuration accepts exactly the two documented lowercase values.

6. The rejection error message must be built with `errors.Errorf(ctx, …)` from `github.com/bborbe/errors` (in-repo precedent: `pkg/gh_cli.go`, `pkg/steps_execution.go`) and must contain:
   - the rejected value (use `%q` so an empty-ish/whitespace value is visible), and
   - every value of `AvailablePRTargets`, rendered by ranging over `AvailablePRTargets` — do NOT hardcode the literal strings `draft`/`ready` in the message.

   Add a `func (p PRTargets) Strings() []string` helper if that makes the message construction cleaner; keep it unexported-friendly (exported is fine, it belongs to the type's API).

7. Create `pkg/pr_target_test.go` in `package pkg_test` (external test package, matching `pkg/url_helpers_test.go`). Ginkgo/Gomega, no new suite file — `pkg/pkg_suite_test.go` already runs the suite. Cover, at minimum:
   - A table/loop over `pkg.AvailablePRTargets` asserting `Validate(ctx)` returns `nil` for every declared value. This is the boundary test: `AvailablePRTargets` is the validation source of truth, so a value added to the collection without being valid must fail here.
   - `ParsePRTarget(ctx, "")` returns `pkg.PRTargetDraft` and no error (spec AC7 — the default remains draft when the setting is absent).
   - `ParsePRTarget(ctx, "draft")` → `pkg.PRTargetDraft`; `ParsePRTarget(ctx, "ready")` → `pkg.PRTargetReady`.
   - `ParsePRTarget(ctx, "bogus")` returns an error whose message contains `bogus`, `draft` AND `ready` (spec AC3 — the message names the offending value and the accepted set).
   - The same rejection assertion for `"Ready"`, `"DRAFT"` and `" draft "` (case-fold / trim are deliberately NOT applied).
   - `PRTargetDraft.IsDraft()` is true; `PRTargetReady.IsDraft()` is false.
   - `PRTargetReady.String()` equals `"ready"`.

8. Append one bullet to the existing `## Unreleased` section of `CHANGELOG.md` (do not replace the section, do not add a version header):
   `- feat: add PRTarget value type (draft | ready) with ParsePRTarget defaulting to draft when unset`
   Read `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` for the entry style.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- Do NOT change any other file: `pkg/gh_cli.go`, `pkg/steps_execution.go`, `pkg/steps_review.go`, `pkg/factory/factory.go`, `main.go` and `cmd/run-task/main.go` are wired in the following prompts. This prompt adds a type and its tests only.
- The default with no configuration present must be byte-identical in observable behaviour to the current release — that is why the empty string resolves to draft rather than erroring.
- The agent must not gain the ability to merge a pull request, or to change the draft-ness of a pull request that already exists. No new capability beyond choosing a target at creation time.
- Do NOT add new module dependencies. `github.com/bborbe/collection` and `github.com/bborbe/validation` are indirect deps and must stay indirect.
- Never `fmt.Errorf`; use `github.com/bborbe/errors`. Never `context.Background()` inside `pkg/` non-test code.
- Existing tests must still pass.
- New code needs ≥80% statement coverage (`/home/node/.claude/plugins/marketplaces/coding/docs/definition-of-done.md`).
</constraints>

<verification>
Run `make test` — all tests pass, including the new `pkg/pr_target_test.go` cases.

Run `make precommit` — must exit 0.

```bash
grep -n 'PRTargetDraft\|PRTargetReady\|AvailablePRTargets' pkg/pr_target.go
# expect: the constants, the collection and their uses

grep -rn 'bborbe/collection\|bborbe/validation' --include='*.go' . | grep -v vendor
# expect: 0 lines — no new direct dependency was introduced

go test -coverprofile=/tmp/cover.out -mod=mod ./pkg/... && go tool cover -func=/tmp/cover.out | grep -E 'pr_target|total'
# expect: ParsePRTarget / Validate / IsDraft at or near 100%
```
</verification>
