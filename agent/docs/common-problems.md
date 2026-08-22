# Common Problems — github-update-go-agent

Recurring failure classes of the Go-update cycle and their deterministic fixes. When planning or
execution hits one of these, apply the fix **autonomously** — do not park the task for the operator.
Each entry states the symptom, the check to confirm it, and the exact fix.

## 1. golangci-lint version incompatible with the new Go

**Symptom**: `make precommit` / `make check` fails at the `lint` step with `buildir: unexpected expr:
*ast.KeyValueExpr`, `export data` errors, or a golangci-lint crash, on a repo targeting the new Go
version.

**Cause**: The repo pins `GOLANGCI_LINT_VERSION` in `tools.env` (or the Makefile) to a golangci-lint
version whose SSA builder (`buildir`, via `honnef.co/go/tools`) does not understand the new Go's AST.
Known broken: v2.11.4, v2.12.2 under Go 1.27. Known good: v2.13.1+.

**Confirm**: `grep GOLANGCI_LINT_VERSION tools.env` shows `< v2.13.1` AND the repo targets Go 1.27+.

**Fix**: Bump the pin in the target repo's `tools.env`:
`GOLANGCI_LINT_VERSION ?= v2.13.1` (keep the `?=` form). Run `make precommit` to confirm the lint
gate is green. This is a per-repo edit; the canonical template lives in `bborbe-go-skeleton/tools.env`.

**Do NOT**: edit `.golangci.yml` to disable linters as a workaround — the version bump is the fix.

## 2. No-fix advisory (empty Fixed Version)

**Symptom**: A scanner (`govulncheck` / `osv-scanner` / `trivy`) reports an advisory with an **empty
Fixed Version** and the module/package is unmaintained, deprecated, or unsafe-by-design. There is no
version to bump to.

**Confirm**: The scanner row's Fixed Version is empty AND the advisory text says "unmaintained",
"deprecated", or "unsafe by design" (e.g. GO-2026-5932 = `golang.org/x/crypto/openpgp`).

**This class parks by design (D4) — do NOT attempt to suppress autonomously.** The planning gate
deterministically parks a task whose plan carries `action: "park"` findings, because the operator's
suppression decision must be made against the real finding (see the `parkMessage` design-D4 comment
in `steps_planning.go`). An agent-side exclusion would hide a vulnerability the operator should
review.

**Your role**:
1. Classify accurately — set `action: "park"` for an empty-Fixed-Version / unmaintained advisory
   (a fixable one is `action: "fix"` and you DO bump it).
2. In the park message, name the exact suppression surfaces the operator will touch:
   `VULNCHECK_IGNORE` (Makefile / Makefile.precommit), `.trivyignore`, `.osv-scanner.toml`
   `[[IgnoredVulns]]` — from the captured scanner table, never fabricated.
3. Do NOT run `add-vuln-ignore.sh` or edit the repo's ignore files — that is the operator's call.

**CRITICAL**: **Never exclude a fixable advisory.** If a Fixed Version exists, bump the dependency —
excluding it hides real risk. Only empty-Fixed-Version / unmaintained advisories may be parked (and
they park, not auto-exclude).

**For the operator** (not the agent): the exclusion procedure is per-repo config edits —
`VULNCHECK_IGNORE` in Makefile/Makefile.precommit, `<GO-ID>` in `.trivyignore`, an `[[IgnoredVulns]]`
block in `.osv-scanner.toml` — see runbook `[[Exclude a No-Fix Vulnerability Across the Fleet]]`.

## 3. Stale pinned task ref

**Symptom**: The clone carries a broken tooling version even though `origin/master` has the fix.

**Cause**: The agent clones the task's pinned `ref` (a commit SHA from when the task was filed). If
that ref predates a `tools.env` / tooling fix (e.g. golangci-lint v2.13.1), the clone has the broken
version — the fix on master never reaches this task's checkout.

**Confirm**: `git -C <workdir> log -1 --oneline` shows the old ref; `git fetch origin && git log -1
origin/master --oneline` shows the fix commit is newer.

**Fix**: Point the work at the current origin head instead of the stale ref:
```bash
git -C <workdir> fetch origin
git -C <workdir> checkout -- .
git -C <workdir> reset --hard origin/master   # or: git checkout <current-origin-head-sha>
```
Then re-run the gate. If the task file's `ref` is itself stale, the fix belongs in the watcher's
ref-resolution — but for this task, re-checking-out current origin head resolves it.

**Do NOT**: keep retrying the same pinned ref — it will fail identically every time.

## 4. DeadlineExceeded jobs

**Symptom**: A job burns the full 30-minute cap and times out instead of failing fast, monopolizing
the executor slot (stalls the whole queue).

**Cause**: A step that should fail fast instead loops/retries until the job deadline, or a gate hangs.

**Confirm**: Job log shows `DeadlineExceeded` / `context deadline exceeded` and the job ran the full
cap.

**Fix**: Fail fast — check `ctx.Done()` before each gate target and each loop iteration; return the
first error instead of retrying past the deadline. If a specific gate hangs (e.g. a long `make
precommit`), bound it with a timeout and classify it as failed-with-findings, not stalled.

**Do NOT**: let the model re-run the same work after a deadline — the executor slot is the scarce
resource.

---

## Usage

- Read this file at the start of **planning** and **execution**.
- When a gate/scanner failure matches a class above, apply the fix and re-run the gate.
- Only park for the operator when a problem has **no known fix** (a genuinely novel failure, or a
  fixable advisory that conflicts with the repo's intent).
- Keep entries machine-checkable: symptom → confirm → fix → do-not.
