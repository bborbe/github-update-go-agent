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

**CRITICAL**: **Never exclude a fixable advisory.** If a Fixed Version exists, bump the dependency —
excluding it hides real risk. Only empty-Fixed-Version / unmaintained advisories may be excluded.

**Fix** (per-repo exclusion — the repo's own config files, exactly what the operator's
`add-vuln-ignore.sh` does):
1. `VULNCHECK_IGNORE ?= <existing> <GO-ID>` in `Makefile` OR `Makefile.precommit` (whichever declares
   it; check both). If the line is multi-line (`\` continuation), append to the **last** continuation
   line — never after a trailing `\`.
2. Append `<GO-ID>` (one per line) to `.trivyignore`.
3. Append an `[[IgnoredVulns]]` block to `.osv-scanner.toml`:
   ```
   [[IgnoredVulns]]
   id = "<GO-ID>"
   reason = "no-fix advisory — unmaintained/deprecated package"
   ```
   If osv-scanner still flags it, add the advisory's `GHSA-…` alias as a second block.
4. Run `make precommit` / `make check` — confirm it prints "No unignored vulnerabilities found" (or
   equivalent) and exits 0.

**Do NOT**: touch `go.mod`/`go.sum` for this — there is nothing to bump. And never invent a
suppression for an advisory that has a fix.

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
