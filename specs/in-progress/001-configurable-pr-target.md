---
status: prompted
approved: "2026-08-14T23:22:14Z"
generating: "2026-08-14T23:23:44Z"
prompted: "2026-08-15T07:31:15Z"
branch: dark-factory/configurable-pr-target
---

# Summary

- The agent always opens pull requests as DRAFT; the target is hardcoded and cannot be configured.
- Every emitted PR therefore needs a manual `gh pr ready` before the review-and-merge pipeline can touch it.
- At fleet scale that promotion step, not the update work, is the bottleneck: one sweep dispatched 84 tasks in a single cycle.
- Introduce a `PR_TARGET` setting with two values, `draft` and `ready`, defaulting to `draft`.
- Unset configuration must behave exactly as today, so the existing safety gate is preserved unless an operator explicitly opts out of it.

# Problem

The agent's PR-creation seam hardcodes the draft flag, and the review phase asserts draft-ness as a success condition. Together they make "the human promotes the PR" a structural property of the agent rather than an operator choice. That was a deliberate safety decision when the agent processed a handful of repos at a time. It stops paying for itself when a single fleet sweep emits dozens of mechanical, gate-green PRs at once: each one still parks awaiting a human to flip it, and the operator's queue grows linearly with fleet size while the agent sits idle. Operators who want these PRs to flow straight into the existing bot-review pipeline currently have no way to say so short of patching the binary.

# Goal

An operator can choose, per deployment, whether the agent opens pull requests as drafts or as ready-for-review, by setting a single configuration value. When the value is absent the agent behaves exactly as it does today: drafts only. When the value selects ready-for-review, the agent opens non-draft pull requests and its own review phase accepts them as valid rather than flagging them as tampered-with. The agent still never merges a pull request and never flips an existing one, under either setting.

# Non-goals

- Merging pull requests. The agent must remain incapable of merging under every configuration.
- Flipping an already-open pull request from draft to ready, or the reverse. The agent chooses a target at creation time only.
- Per-repository or per-task selection of the target. The setting is per-deployment. Introducing a task-level override would require the upstream watcher and trigger to emit a new field and is deliberately deferred.
- Changing which repositories the agent updates, what it updates in them, or how it decides that work is needed.
- Changing how many update jobs run concurrently.

# Acceptance Criteria

- [ ] With no PR-target configuration present, the agent creates a draft pull request — evidence: the created pull request reports draft as true when queried, and the command the agent invoked contains the draft flag.
- [ ] With the PR target set to ready, the agent creates a non-draft pull request — evidence: the created pull request reports draft as false when queried, and the command the agent invoked does not contain the draft flag.
- [ ] The configuration accepts exactly the two documented values and rejects anything else, with a message naming the offending value and the accepted set — evidence: a unit test on the parse function returns an error for an unrecognised value whose message contains the rejected value and both accepted values. Note the binary cannot be used for this check: task content is a required field, so an otherwise-empty invocation exits non-zero on that field before target validation runs.
- [ ] The review phase approves a pull request whose draft-ness matches the configured target — evidence: `ReviewOutput.Approved == true` and `ReviewOutput.Notes` contains no draft-mismatch substring, for the draft/draft and ready/ready pairings.
- [ ] The review phase declines to approve, and records a note, when draft-ness does not match the configured target — evidence: `ReviewOutput.Approved == false` and `ReviewOutput.Notes` (a string) contains a substring naming both the observed and the configured draft-ness, for the draft/ready and ready/draft pairings. Exact wording — agent decides at impl time.
- [ ] The configured target reaches the review phase through the **production** wiring, not a test-only shim — evidence: the test drives `factory.CreateAgent` (the production constructor, not a new helper) with the target set to ready and observes a non-draft pull request as passing; **and** the single-call-site lock below returns exactly **1** line, so no parallel construction path was introduced.

  ```bash
  grep -rn 'NewReviewStep(' --include='*.go' . | grep -v vendor | grep -v _test.go | grep -v 'func NewReviewStep'
  ```

  The final filter is required — without it the function *declaration* at `pkg/steps_review.go:43` also matches and the count is 2, making the criterion unsatisfiable. Verified against the repo on 2026-08-15: returns 1.
- [ ] The default remains draft when the setting is absent — evidence: a unit test asserts the resolved target equals draft when the configuration value is the empty string.
- [ ] The agent gains no ready-flip or merge capability — evidence: `grep -rn 'pr ready\|pr merge' --include='*.go' . | grep -v vendor` returns 0 lines, and `pkg/gh_cli.go` contains no `gh pr` argument list carrying `ready` or `merge`.
- [ ] Every location asserting the unconditional never-ready claim describes the configurable behaviour instead. **Eight sites across five files** (each verified against the repo on 2026-08-15):

  | File | Line | Current wording |
  |---|---|---|
  | `README.md` | 3 | "never readies or merges the PR" |
  | `pkg/gh_cli.go` | 26 | "never flips a draft and never merges; the human does" |
  | `pkg/gh_cli.go` | 71 | "`--draft` is hardcoded: the agent opens drafts only" |
  | `pkg/review_output.go` | 13 | "the agent never readies" |
  | `pkg/steps_review.go` | 151-152 | "the agent never / readies" — **wrapped across a line break** |
  | `docs/design.md` | 110 | "Human reviews + readies the draft" — the downstream flow, which changes under a ready target |
  | `docs/design.md` | 201 | "never-tag/never-push/never-ready is structural" |
  | `docs/design.md` | 203 | "No ready/merge: PR creation via `gh pr create --draft` only" |

  Evidence — this command must return **0** lines (verified: returns 8 before the change):

  ```bash
  grep -rniE 'readies|never-ready|no ready/merge|--draft is hardcoded|never flips a draft' \
    --include='*.go' --include='*.md' . | grep -v vendor | grep -v '^specs/' | grep -v '^prompts/'
  ```

  > *Scope correction 2026-08-15, after prompt generation:* the `^prompts/` exclusion is required — the implementation prompts quote the forbidden wording **verbatim as instructions to remove it** (measured: prompt 2 carries 2 hits, prompt 3 carries 3, prompt 4 carries 10; without the exclusion the command returns 25 pre-change and can never reach 0, because completed prompts stay committed). This fixes the evidence command's scope, not the criterion: the eight *product* sites are unchanged.

  Two details are load-bearing. The bare token `readies` is deliberate: a `never readies` pattern misses the wrapped `steps_review.go` occurrence, where line 152 *begins* with `readies`. And the exclusion is `^specs/`, not `^./specs/` — `grep -rn .` prints paths without a `./` prefix, so the dotted form silently matches nothing and the spec file's own table pollutes the count.

  Additionally, the §7.0 capability-removal record gets a dated reversal marker rather than a silent edit — `grep -c 'reversed 2026-08' docs/design.md` returns ≥1 (verified: returns 0 before the change). A bare `grep '2026-'` would be a no-op: the file already contains 9 such lines.
- [ ] `checks.pr_draft` in the review output keeps reporting raw observed draft-ness, not match-against-target — evidence: a test asserts `ReviewOutput.Checks.PRDraft == false` for a non-draft pull request even when the configured target is ready and `ReviewOutput.Approved == true`.
- [ ] `make precommit` exits 0.
- [ ] **Post-Deploy (Rung-2):** with the target set to ready in dev, one update task opens a non-draft pull request and the task reaches the human-review phase — evidence: `gh pr view <n> --json isDraft` returns `false`.
  - `deploy_check:` `kubectlquant -n dev get configs.agent.benjamin-borbe.de agent-github-update-go -o jsonpath='{.spec.image}' | awk -F: '{print $NF}'`
  - `deploy_target:` `$(git describe --tags --abbrev=0)`

  This criterion additionally depends on the cross-repo Helm edit described in the operator verification rung — `PR_TARGET` must be present in the dev Config resource's environment before it can pass. A missing setting means the enabling step was skipped, not that the implementation failed.

# Verification

## Container-executable (runs inside the YOLO container at prompt time)

- `make precommit` — lint, vet, and the full test suite pass.
- `make test` — unit tests pass, including the new target-resolution and review-phase cases.
- `grep -n 'PR_TARGET' main.go` — returns at least one line, confirming the setting reached the configuration struct.
- `grep -n 'PRTarget' pkg/gh_cli.go` — returns at least one line, confirming the creation seam takes the target rather than hardcoding the flag.

## Operator-executable (runs on the host after PR merge)

This agent has **no Deployment**. It runs as short-lived Jobs spawned from an agent Config custom resource, so both the image and the environment live on that resource:

- `kubectlquant -n dev get configs.agent.benjamin-borbe.de agent-github-update-go -o jsonpath='{.spec.image}'` — dev runs the new release tag.
- Enabling ready in dev is a **cross-repo change**: the Config CR is Helm-managed (release `agent-dev`), so `PR_TARGET: ready` is added to `spec.env` in the **agent Helm chart repo** and re-applied. Nothing in this repo can set it.
- With dev configured for ready, run one update task; `gh pr view <n> --json isDraft` returns `false` and the task reaches the human-review phase rather than parking with a mismatch note.
- With prod left unconfigured, run one update task; `gh pr view <n> --json isDraft` returns `true`.

# Desired Behavior

1. A configuration value named for the pull-request target is read at startup from the environment, alongside the agent's existing settings, and defaults to draft when unset or empty.
2. The value is validated at startup. An unrecognised value stops the agent immediately with an error naming both the rejected value and the accepted set, rather than being silently coerced to a default.
3. The pull-request creation seam takes the target as an argument instead of hardcoding it, and includes the draft flag only when the target is draft.
4. The resolved target is threaded from configuration through the agent's wiring to both the creation seam and the review phase, so a single value governs both.
5. The review phase compares the observed draft-ness of the pull request against the configured target and treats a match as passing, replacing its previous unconditional expectation of draft.
6. When the observed draft-ness does not match the configured target, the review phase declines to approve and records a note describing the mismatch, preserving today's behaviour of surfacing an unexpected pull-request state to the operator.

# Assumptions

- The agent Config resource's environment map is free-form, so exposing the setting needs no change in the executor that spawns the Jobs. The goal is reachable from this repo alone; only *enabling* ready requires the separate Helm-chart edit noted in the operator verification rung.
- All three phases — planning, execution, and review — resolve to the same Config resource, so every phase Job inherits one value. This is what makes "a single value governs both the creation seam and the review phase" true at runtime rather than only in-process.

# Constraints

- The default with no configuration present must be byte-identical in observable behaviour to the current release. This is the load-bearing constraint: an operator who upgrades and changes nothing must see no difference.
- `pr_draft` in the review output keeps its current meaning — raw observed draft-ness. The match-against-target decision lives in the approval flag and the notes, so the serialized key does not silently change meaning for downstream consumers that round-trip it into task frontmatter.
- The agent must not gain the ability to merge a pull request, or to change the draft-ness of a pull request that already exists. No new capability beyond choosing a target at creation time.
- The configuration must follow the same declarative mechanism the agent already uses for its other settings, so it appears in the generated usage output alongside them.
- The existing mock for the pull-request seam is generated, not hand-written. It must be regenerated from the changed interface rather than edited by hand.
- The review phase's other checks — the pull request being open, the repository gate passing, the changelog entry, and the absence of a new tag — must keep their current semantics.
- Documentation that currently states the agent never opens a ready pull request must be corrected at all eight sites across five files (`README.md`, `pkg/gh_cli.go` ×2, `pkg/review_output.go`, `pkg/steps_review.go`, `docs/design.md` ×3 — enumerated in the acceptance criteria). The design document's capability-removal record gets a dated revision note rather than a silent edit, since it records a decision that is being deliberately reversed.
- The creation seam's name must stop implying draft-only once it is parameterized. The interface method is renamed accordingly; the generated mock is regenerated to match.

# Failure Modes

| Trigger | Expected behavior | Detection | Reversibility | Recovery |
|---|---|---|---|---|
| Configuration set to an unrecognised value | Startup fails with a non-zero exit and an error naming the value and the accepted set | Job exits immediately; no pull request created | Fully reversible — nothing was written | Operator corrects the value and re-runs the task |
| Configuration set to ready, but the platform credential lacks permission to open a non-draft pull request | The creation command fails and the error is surfaced verbatim, not swallowed | Task fails at the creation step with the underlying error text | Reversible — no pull request exists | Operator grants the permission or reverts the setting to draft |
| Target is ready and a human flips the pull request back to draft before the review phase runs | The review phase declines to approve and records a mismatch note | Task parks for the operator with the note attached | Reversible — the pull request is untouched by the agent | Operator flips it back or accepts the parked task |
| Deployment is upgraded but the setting is never configured | The agent behaves exactly as the previous release | No observable change | Not applicable | None required |
| Two deployments in different environments hold different targets | Each behaves according to its own configuration; neither reads the other's | Environment-scoped configuration is independent by construction | Not applicable | None required |

# Security / Abuse

The setting is read from the deployment's own environment, which is already the trust boundary for the agent's credentials. It grants no capability the agent lacks today beyond omitting a flag at creation time; in particular it cannot cause a merge, cannot alter an existing pull request, and cannot widen which repositories are reachable. Choosing ready does remove a human checkpoint before the review bot sees the pull request, so it is opt-in and defaults off — an operator must take an explicit action to accept that.

# Suggested Decomposition

Prompts should be generated in this order — each row is a single prompt with a clear scope.

| # | Prompt focus | Covers DBs | Covers ACs | Depends on |
|---|---|---|---|---|
| 1 | Target value type, parsing, validation, and default-to-draft resolution, with unit tests | 1, 2 | 3, 7 | — |
| 2 | Rename and parameterize the pull-request creation seam on the target; regenerate the mock; update the single call site | 3 | 1, 2, 8 | prompt 1 |
| 3 | Thread the target through configuration and wiring into both the creation seam and the review phase, with a factory-constructed test | 4 | 6 | prompts 1, 2 |
| 4 | Review phase compares observed draft-ness against the configured target; tests for all four pairings; `checks.pr_draft` keeps raw semantics | 5, 6 | 4, 5, 10 | prompts 1, 3 |
| 5 | Documentation: correct the capability claims at all **eight** sites (five files) and describe the setting | — | 9 | prompts 1-4 |

AC 11 (`make precommit`) is cross-cutting — every prompt must leave it green. AC 12 is the post-deploy check, verified by the operator after release, not by any prompt.

**Size-budget overage accepted deliberately.** 6 desired behaviours × 12 acceptance criteria = 72 against the guide's threshold of 50, across roughly five code layers against a threshold of three. Splitting is rejected: the load-bearing constraint — that an unset configuration is byte-identical to today — spans both the creation seam and the review phase, so neither half of a split spec could verify that guarantee on its own. This decomposition table is the mitigation.

Rationale: prompt 1 establishes the value type everything else consumes; prompt 2 changes the outbound seam; prompt 3 connects configuration to both consumers and is the one that proves the wiring rather than a detached helper; prompt 4 changes the inbound assertion; prompt 5 reconciles the written record once behaviour is settled.

# Do-Nothing Option

Leaving the target hardcoded costs one manual promotion per emitted pull request. At the current fleet size a single sweep produced 84 of them, so the operator absorbs 84 promotions before the review-and-merge pipeline does any work — and that cost scales with the fleet, recurring on every toolchain release. The alternatives to this change are worse: patching the binary per deployment forks the agent, and scripting a bulk promotion outside the agent re-implements a decision the agent is already making, in a place where it cannot be tested. The counter-argument for doing nothing is real but narrow — the draft step is a deliberate human checkpoint, and removing it everywhere would be a regression. Making it configurable and defaulting it to the current behaviour keeps that checkpoint for anyone who wants it while letting fleet-scale operators opt out deliberately.
