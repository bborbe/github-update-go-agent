---
status: draft
page_type: knowledge
tags:
  - knowledge-base
  - agent
  - design
date: 2026-07-21
---
Tags: [[GitHub Update Go Agent]] [[Agent Design Guide]] [[GitHub Update Go Agent - Base]] [[Go Agent Implementation Guide]] [[Agent Task File Contract]]

---

Filled-in [[Agent Design Guide]] design doc for `github-update-go-agent` (Stage-2 MVP, goal [[GitHub Update Go Agent - Base]]). **Vault staging copy** — moves to `github-update-go-agent/docs/design.md` at scaffold time; this page then becomes a stub pointer (repo-resident design pattern, per [[Agent Hub]]).

## Key decisions (with rejected alternatives)

| # | Decision | Alternative rejected | Why |
|---|---|---|---|
| D1 | **Claude-based mixed shape** (rev. 2026-07-21 operator review): `agent-claude` template as base; Claude planning + Claude execution steps (distinct prompts + per-phase tool scopes, pr-reviewer style); **pure-Go ai_review** | Pure-Go throughout (agent/code shape) — first draft | Pure-Go parks on exactly the high-value cases: dep bumps that break APIs/tests, vuln fixes needing code edits. LLM execution repairs to green — the prototype's observed strength (fleet: 35/44 auto-fixed). ai_review stays deterministic Go: checks are mechanical and a fresh non-LLM verifier cannot rubber-stamp |
| D2 | **No Python `updater` in the image** — the execution prompt embeds `updater`'s validated `--no-git` step sequence (version bump → excludes → `go get -u`+tidy → targeted `go get @fixed` → OSV fix → indirect cleanup → gate) as instructions Claude executes with native go/git/make commands, repairing breakage as needed | Bundle Python `updater` (needs Python 3.14 + uv + `ANTHROPIC_API_KEY`) in the image | Lean image; the sequence is the validated subset; `updater`'s git-mode features (changelog finalize, tag) are *banned* here anyway; Claude executing the sequence directly can also fix what the mechanical tool cannot |
| D3 | **GitHub App pair per stage, least-privilege**: Contents R/W + Pull requests R/W + Metadata R, **no Workflows** | Reuse an existing App / user PAT | Capability removal beats prose (§7.0): agent physically cannot touch `.github/workflows/`; PR write needed because this agent *creates* draft PRs (delta vs dark-factory-agent which only pushes) |
| D4 | **Unfixable vuln → park whole task at planning** (`needs_input` naming the CVEs) | Auto-suppress via `.trivyignore`/`.osv-scanner.toml`; or fix-the-fixable + open PR with red gate | Suppression is a human risk call (learning #7). Partial-fix PR impossible: un-suppressed finding keeps the gate red and no PR opens on red. Operator suppresses with justification, re-delegates; agent then sees a clean classification and proceeds |
| D5 | **Target Go version = toolchain baked into the image** | Fetch `go.dev/VERSION` at runtime; trust watcher's `latest_go` frontmatter | Deterministic, offline; the image is itself kept current by this very pipeline. Watcher's `latest_go` stays a trigger signal only |

## Suppression surfaces (fleet convention — validated on go-skeleton 2026-07-21)

Three scanners, three ignore surfaces; a single unfixable finding may need entries in **up to all three** (real example: `GO-2026-5932` openpgp appears in all three files in `bborbe/go-skeleton`):

| Scanner | Gate target | Ignore surface | Format |
|---|---|---|---|
| govulncheck | `make vulncheck` | `Makefile.precommit` → `VULNCHECK_IGNORE ?= <ids>` | space-separated `GO-*` IDs (Make variable) |
| osv-scanner | `make osv-scanner` | `.osv-scanner.toml` | `[[IgnoredVulns]]` with `id` + `reason` |
| trivy | `make trivy` | `.trivyignore` | one `CVE-*`/`GHSA-*`/`GO-*` per line, `# reason` comment |

MVP consequences (D4): the agent **reads** these (the gate already respects them) but **never writes** them — on an unfixable finding, planning parks with a message naming the finding ID, the scanner(s) that flagged it, the exact file(s) an operator-approved suppression would touch, and a pointer to [[Exclude a No-Fix Vulnerability Across the Fleet]] (whose idempotent helper `add-vuln-ignore.sh` patches all three surfaces; note `VULNCHECK_IGNORE` may live in `Makefile` OR `Makefile.precommit`, and osv-scanner sometimes needs the `GHSA-*` alias as a second block). Stage-3 candidate: agent applies the suppression itself via the helper under operator pre-approval — not MVP.

## Execution repair playbook (embedded in `pkg/prompts/execution.md`)

Distilled from [[Go Vulnerable Indirect Dep - Bump Parent vs Pin]], [[Go Directive Pins CI Toolchain for Stdlib Vulncheck]], and `coding/docs/go-mod-dependency-fix-guide.md`:

1. **Tidy after every `go get`** (MUST) — `go get` leaves the MVS graph inconsistent; `go mod tidy` is the canonical reconcile. Verify with `make test`, never `go build ./...` alone.
2. **Vulnerable indirect dep → bump the parent, not a bare pin.** `go mod graph | grep <pkg>` → find parent → verify in a /tmp throwaway module that the newer parent drops the chain → bump parent. Bare `// indirect` pins are silently removed by the next tidy (MVS regresses to the vulnerable version — real incident: auth-http-proxy CVE-2026-46680 re-fired after `go mod tidy`).
3. **Double-tidy litmus** — after any vuln fix: `go mod tidy && ! grep -q '<vuln-pkg>' go.mod` run twice; if the second tidy reintroduces it, escalate to `replace`/`exclude` with a CVE-naming comment.
4. **Prefer `exclude` over cross-repo `replace`** for skipping a broken version (MVS picks the next valid one); `replace` only for redirect semantics.
5. **Stdlib CVEs are toolchain-relative** — CI pins to the `go.mod` directive (`go-version-file: go.mod`); a newer pod toolchain hides stdlib findings CI will flag. Therefore the directive bump to the image toolchain (D5) is mandatory in every PR, plus the Dockerfile `golang:` base — "found in <pkg>@go1.X.Y where X.Y = directive" is the tell.
6. **Broken transitive pre-release after `go get -u`** — try `go mod tidy` first, then bump the *direct* deps that pull it; forced downgrades cascade — don't fight MVS.

# 1. Motivation

## 1.1 Problem
The Stage-1 prototype removed per-repo manual work but the operator must still *invoke* `/github-update-go-task-agent` once per task — at fleet scale (2026-07-19 sweep: 44 tasks) the invocation loop is the new bottleneck, keeping the operator's laptop and attention in the hot path for hours.

## 1.2 Current manual alternative
Run a watcher command → for each emitted task run the agent slash command → shepherd. Steps 1–2 are mechanical; only review+merge needs a human.

## 1.3 Do-nothing cost
~2–5 min operator attention per task × fleet sweeps (~44 tasks/sweep, monthly-ish) + serialization on one laptop session; vuln fix latency tied to operator availability (same-day security response target at risk).

## 1.4 Success measures (1 month post-deploy)
Tasks emitted by any watcher command become draft PRs with **zero** agent invocations; parked tasks carry precise reasons; operator time per sweep drops to review+merge only (~1–2 min/PR, per [[Update or Fix GitHub Go Repositories]]).

# 2. Identity

- **2.1 Name:** `github-update-go` — assignee `github-update-go-agent`, image/repo `github-update-go-agent`, CRD `agent-github-update-go`. Assignee unique fleet-wide.
- **2.2 Purpose:** Consumes `github-update-go` tasks to land a reviewable draft PR that brings a Go repo to current toolchain + dependencies with zero open fixable vulnerabilities and a green repo gate.
- **2.3 Repo:** standalone `bborbe/github-update-go-agent`, cloned from the `agent-claude` template (post-monorepo-split convention; sibling of `github-dark-factory-agent`).
- **2.4 Pattern:** B (ephemeral Job) on `lib.NewAgent`, `bborbe/agent v0.72+`.
- **2.5 Runtime:** Claude Code CLI (planning + execution) + pure Go (ai_review, preflights, plumbing) — D1. Image = `agent-claude` Dockerfile shape (alpine + go toolchain + make/git/gh + scanners + nodejs/claude-code); `CLAUDE_CONFIG_DIR` matches image `HOME` (`/home/claude/.claude`), OAuth via PVC per template.
- **2.6 Task types:** `github-update-go` + `healthcheck` + `oauth-probe` (Claude OAuth present → probe it, per agent-claude template).

# 3. Integration

## 3.1 Trigger
Stage-2 primary: existing Stage-1 emit commands (`/github-update-go-repo-watcher`, `/github-update-go-vuln-watcher`, `/github-update-go-repo-trigger`) — operator-fired, emitting to `24 Tasks/`; controller → Kafka → executor → Job. Stage-3: `repo-update-watcher` Go service (non-goal here).

## 3.2 Task producer
The three slash commands; dedup via deterministic `task_identifier` from `(owner, repo, head_sha)` — Stage-1 SHA1-shaped, Stage-2 accepts both; new emissions may move to UUID5 via lib helper when the watcher graduates (contract-compatible: field stays deterministic).

## 3.3 Task format
Per [[Agent Task File Contract]] — 7 required fields plus domain fields (unchanged from Stage 1):

```yaml
task_type: github-update-go
assignee: github-update-go-agent
phase: planning
status: in_progress
stage: dev
task_identifier: <deterministic from (owner, repo, head_sha)>
title: Update Go <owner>/<repo> at <sha:7>
repo: <owner>/<repo>
clone_url: git@github.com:<owner>/<repo>.git   # agent rewrites to https for App-token auth
ref: <full HEAD SHA>
current_go: <X.Y.Z>   # watcher signal only
latest_go: <X.Y.Z>    # watcher signal only (D5: agent targets its image toolchain)
```

Body = operator-readable header only; never a data source.

## 3.4 Upstream dependencies

| Dependency | Availability | Failure mode |
|---|---|---|
| GitHub API + git remote (App IAT) | hard | `failed` — controller parks |
| Kafka (result delivery) | hard when `TASK_ID` set | Job exit non-zero → redeliver |
| Target repo Makefile w/ gate target(s) | hard | `needs_input` "no gate target found" |
| Scanner binaries in image (trivy, osv-scanner, govulncheck) | hard (baked) | image-build-time failure, not runtime |

## 3.5 Downstream consumers
Human reviews + readies the draft (runbook [[Update or Fix GitHub Go Repositories]] step 3) → `pr-review-agent` (step 4) → human merge (step 5) → `github-releaser-agent` versions + tags on merge (step 6). Parked tasks surface via `assignee == ""` operator inbox.

# 4. Behavior

## 4.1 Supported statuses
`[in_progress]` (default).

## 4.2 Supported phases

| Phase | Supported | Step impl | Purpose |
|---|---|---|---|
| `planning` | yes | preflights (claude-auth, gh-token — lift from dark-factory-agent) + `claude.NewAgentStep` w/ planning prompt | clone, detect gate targets, run scanners, classify findings (fix / park), write `## Plan` |
| `execution` | yes | preflight (claude-auth) + `claude.NewAgentStep` w/ execution prompt | run update sequence, **repair breakage to green** (bounded), commit, push, draft PR, write `## Result` |
| `ai_review` | yes | custom pure-Go `VerifyStep` | independently verify PR + gate + scanners, write `## Review`, route `human_review` |

## 4.3 Per-phase decisions

**planning**
| Decision | Value |
|---|---|
| Input | frontmatter `repo`, `clone_url`, `ref` |
| Output | `PlanOutput{go_bump{from,to}, dep_updates_expected bool, gate_targets []string, vulns []{id, package, fixed_version, action fix\|park, reason}, has_work bool}` → `## Plan` |
| Side effects | bare-clone + worktree @ ref (read-only wrt origin); detect gate targets from Makefile (`precommit`, `check`, `vulncheck`); run the repo's own scanner targets; enumerate outdated deps |
| Allowed tools | `Read, Grep, Glob, Bash(git:*), Bash(go:*), Bash(make:*)` — no Edit/Write, no push |
| Model | Sonnet |
| Prompt module | `pkg/prompts/planning.md` — lifted from the slash command's planning section + learning #3 (repo's own multi-scanner gate, never hardcoded `govulncheck`) |
| Duration | < 5 min |
| Next on success | `execution` (has_work) · `done`+`status: completed` (`no_update_needed`) |
| Failure | any `park`-action vuln → `needs_input` naming CVEs (D4); nested/multi-module root → `needs_input`; clone/auth fail → `failed` |
| Preconditions | required frontmatter present; single Go module at repo root |
| Postconditions | `## Plan` JSON present; phase advanced |

**execution**
| Decision | Value |
|---|---|
| Input | frontmatter + `## Plan` (via `ExtractSection[PlanOutput]`) |
| Output | `ResultOutput{outcome, branch, pr_url, gate_exit int, deps_updated int, vulns_fixed []string}` → `## Result` |
| Side effects | worktree @ ref; `git switch -c fix/update-go-<sha:7>`; **update sequence per D2** (go-directive bump targeting image toolchain (D5) → excludes/replaces → `go get -u ./...` + `go mod tidy` → targeted `go get <pkg>@<fixed>` per plan → OSV/indirect cleanup) → **repair to green (bounded)**: Claude may edit code to fix compile/test breakage *caused by the bumps* (never unrelated refactors) → CHANGELOG bullet under `## Unreleased` → run ALL detected gate targets to exit 0 → commit (no attribution) → `git push --no-follow-tags -u origin <branch>` → `gh pr create --draft` |
| Allowed tools | `Read, Grep, Glob, Edit, Write, Bash(git:*), Bash(go:*), Bash(make:*), Bash(gh pr create:*), Bash(gh pr view:*)` — no `gh pr ready/merge`, no `git tag` |
| Model | Sonnet |
| Prompt module | `pkg/prompts/execution.md` — the slash command's execution section incl. the release-agent integration block (`## Unreleased` only, never tag) + repair-scope bounds |
| Duration | 5–20 min (repo test suites) |
| Next on success | `ai_review` |
| Failure | gate red after pipeline → `failed` with tail of failing output (resume cursor = execution); push/PR fail → `failed` |
| Preconditions | `## Plan` exists with `has_work: true` |
| Postconditions | draft PR open; `## Result` present; no tag created anywhere |

**ai_review**
| Decision | Value |
|---|---|
| Input | `## Plan` + `## Result` |
| Output | `ReviewOutput{approved bool, checks{pr_open, pr_draft, gate_green, vulns_clear, changelog_unreleased, no_new_tag}, notes}` → `## Review` |
| Side effects | `gh pr view --json state,isDraft`; fresh worktree @ branch; re-run gate targets; verify CHANGELOG bullet under `## Unreleased` and no new `## vX.Y.Z` header; `git ls-remote --tags` shows no tag at branch commits |
| Duration | 5–15 min |
| Next on success | `human_review` (the ONLY writer of that phase; success semantics per doctrine) |
| Failure | any check false → `## Review` with `approved: false` + `Status: failed` (controller parks; body keeps the verdict) |
| Preconditions | `## Plan` + `## Result` exist |
| Postconditions | `## Review` present |

## 4.4 State passing + invariants
`## Plan` / `## Result` / `## Review` as typed JSON via `agentlib.MarshalSectionTyped` / `ExtractSection[T]` (never `strings.Index`). Invariants: `Result.branch == "fix/update-go-" + ref[:7]`; `Result.vulns_fixed ⊆ {v.id | v ∈ Plan.vulns, action=fix}`; `Review.checks.gate_green` derived from re-execution, not from `Result.gate_exit`.

## 4.5 Non-goals
Per goal: no watcher service, no auto-merge/ready, no auto-suppress, no NPM/Python, no major Go bumps (1.x→2.0 → `needs_input`), no trading monorepo, no full `updater` port, no capabilities beyond the prototype.

# 5. Data Contract

## 5.1 Inputs
Task frontmatter (Kafka `TASK_CONTENT`); target repo via git clone (App IAT over HTTPS); repo Makefile (gate detection); scanner outputs (parsed for finding id + fixed-version); `go list -u -m` output.

## 5.2 Outputs
Branch + draft PR on target repo; task body sections; `AgentResult` JSON on stdout (executor round-trips to frontmatter via Kafka).

## 5.3 Idempotency
`ShouldRun` always true; replay guard inside `Run` (dark-factory pattern): existing `## <Section>` → re-route without redoing side effects. Execution crash-window guard: branch name is deterministic — if `gh pr list --head fix/update-go-<sha:7>` finds an open PR, adopt it and write `## Result` instead of re-pushing. Same task replayed → same branch, same PR, no duplicates.

## 5.4 Concurrency
Executor: one Job per (task, phase). Fleet sweeps parallelize across tasks — bounded by namespace ResourceQuota. Per-repo serialization guaranteed by task dedup on `(owner, repo, head_sha)`.

# 6. Operations

- **6.1 Scheduling:** task-driven only (no cron in the agent).
- **6.2 Resources:** CPU 2 / mem 4Gi / ephemeral 10Gi (Go build+test+module cache + Claude config) / Job activeDeadline 30 min. Higher than plain Claude agents — this pod compiles and tests real repos.
- **6.3 Cost:** ~100–300k tokens/task (Sonnet, planning + execution incl. repair loops) ≈ $0.5–1.5/task; a 44-task sweep ≈ $20–60/month at monthly cadence — acceptable vs ~3h operator time. ai_review costs zero tokens (pure Go).
- **6.4 Observability:** glog per phase; pushgateway `libmetrics.NewJobMetrics` grouped `agent`+`task_type` (standard); log lines include repo + phase + gate target + exit.
- **6.5 Kill switch:** `kubectlquant -n <stage> delete config agent-github-update-go` (< 2 min; no Jobs spawn thereafter).
- **6.6 Rollout:** dev CRD → e2e on one triggered task → deferred-path exercises (goal SC3) → ≥3 clean runs → prod CRD.

# 7. Safety

## 7.0 Consent gates (capability removal)
- No tag can leak: agent never runs `git tag`; the only push helper hardcodes `--no-follow-tags` (learning #2).
- No ready/merge: GitHub client is read-only for PR state; PR creation via `gh pr create --draft` only; no code path invokes `gh pr ready`/`gh pr merge` (structural, mirrors dark-factory-agent).
- No workflow edits: App lacks Workflows permission (D3) — push touching `.github/workflows/` fails at origin.

## 7.1 Error handling
Per platform doctrine (spec 039): agent emits `Status: failed`/`needs_input` + message; **controller** owns the envelope (clear `assignee`, set `previous_assignee: github-update-go-agent`, append `## Failure`; phase+status untouched). Agent never writes `## Failure`, never mutates assignee/status, never emits `NextPhase: human_review` on failure. Concrete scenarios:
- Unfixable CVE found → planning `needs_input` "CVE-X, CVE-Y have no fixed version — suppress with justification or hold" (D4)
- Gate red after full pipeline → execution `failed` + failing target/tail (resume = execution, learning #6)
- Clone 401/403 → `failed` "git auth failure — check App installation for <repo>"
- Nested module (`backup → service/`) → planning `needs_input` (operator handles; out of scope)
- Retry cap: default 3/phase (controller-owned).

## 7.2 Security
- **App auth (7.2a/7.2b):** App pair per stage — Contents R/W, Pull requests R/W, Metadata R, no Workflows, webhook off; dev installed on canary repo only, prod on `bborbe/*`.

  | Stage | App | App ID | Installation ID | PEM |
  |---|---|---|---|---|
  | dev | `Ben's Go Updater Dev` (created 2026-07-21; installed on `bborbe/go-skeleton` only) | `4355227` | `148020867` | [TeamVault VOzzoO](https://teamvault.benjamin-borbe.de/secrets/VOzzoO/) |
  | prod | `Ben's Go Updater` — create at prod-deploy time | — | — | — | PEM in Teamvault; App ID / Installation ID in CRD env. Ephemeral Job mints IAT once at startup: `resolveAuth` (lift from `github-dark-factory-agent` `main.go:264` — `githubapp.MintIAT` on `context.WithTimeout(WithoutCancel(ctx), 30s)`), then `os.Setenv("GH_TOKEN", …)` + `githubauth.NewGhAuthSetupGit(...)` with allowlisted subprocess env (`GH_TOKEN`,`HOME`,`PATH`). Raw `GH_TOKEN` accepted only for local `cmd/run-task`.
- **Arbitrary code execution is inherent:** the gate runs the target repo's Makefile in-pod with `GH_TOKEN` in env. Acceptable: own repos at master HEAD only. Future hardening: NetworkPolicy egress ([[Improve Agent Security]]).
- No secrets in task bodies or PR bodies; token scrubbed from errors (lift `scrubToken`).
- **Claude auth:** OAuth config on PVC mounted at `CLAUDE_CONFIG_DIR` = image `HOME/.claude` (`/home/claude/.claude`, agent-claude template); `oauth-probe` task type + [[Agent - Refresh Claude OAuth Login]] runbook apply; `GH_TOKEN` threaded into the Claude subprocess env (allowlisted) so `gh pr create` works inside the step.

## 7.3 Assumptions
Target repo: single Go module at root; Makefile with ≥1 gate target among `precommit`/`check`/`vulncheck` (fleet shape: `check: lint vet errcheck vulncheck osv-scanner gosec trivy`); CHANGELOG.md with `## Unreleased` convention; `.maintainer.yaml` releaser integration (versioning on merge). **Tooling:** most scanners/linters run via `go run tool@$(VERSION)` from the repo's Makefile — the pod needs GOPROXY egress + module cache space, and the image needs only **trivy** (system binary), `jq`, `column`, plus go/make/git/gh/node+claude-code. Violations → `needs_input`, not crash.

## 7.4 Data privacy
Own public/private repos; no third-party data; no LLM provider involved (D1).

# 8. Acceptance

## 8.1 Per-phase
- planning: `## Plan` valid `PlanOutput` JSON on a real repo; park path fires on synthetic `Fixed: N/A` finding
- execution: draft PR open, gate exit 0 on branch, CHANGELOG bullet under `## Unreleased`, `git ls-remote --tags` unchanged
- ai_review: `## Review` all-true on the happy path; seeded broken check (e.g. deleted PR) → `approved: false` + park

## 8.2 Overall
Goal [[GitHub Update Go Agent - Base]] SC1–SC5 + DoD verbatim (autonomous e2e, prototype parity, deferred paths, replay idempotency, metrics; design doc committed, CRD dev→prod, kill switch rehearsed, App pair created, vault synced).

## 8.3 Verification
```bash
cd github-update-go-agent && make precommit                     # unit + build
GH_TOKEN=… TASK_FILE=cmd/run-task/dummy-task.md go run ./cmd/run-task --phase planning
# dev e2e: /github-update-go-repo-trigger bborbe/go-skeleton → observe Job:
kubectlquant -n dev logs job/agent-github-update-go-<id>
gh pr view <n> --repo bborbe/go-skeleton --json state,isDraft   # OPEN + true
```

## 8.4 Rollback
Kill switch (6.5) → revert CRD image tag → remove CRD + deprecate assignee.

# Related

- Goal: [[GitHub Update Go Agent - Base]] · Identity: [[GitHub Update Go Agent]] · Learnings: [[GitHub Update Go Prototype Learnings]]
- Guides: [[Agent Design Guide]] · [[Go Agent Implementation Guide]] · [[Agent Task File Contract]] · [[Agent Phase Dispatch Guide]] · [[Mixed-Shape Pattern B Agents]] (D1 is a textbook instance: Claude where judgment/repair pays, pure-Go verifier)
- Source repos: `agent-claude` (skeleton) · `github-dark-factory-agent` (auth/git/push lift) · `updater` (pipeline semantics ported in D2)
- Repair-playbook sources: [[Exclude a No-Fix Vulnerability Across the Fleet]] (park→suppress operator flow + `add-vuln-ignore.sh`) · [[Go Vulnerable Indirect Dep - Bump Parent vs Pin]] · [[Go Directive Pins CI Toolchain for Stdlib Vulncheck]] · `coding/docs/go-mod-dependency-fix-guide.md` · [[Go - Update Version]] · [[Go - Update Single Dependency in Multi-Module Project]]
