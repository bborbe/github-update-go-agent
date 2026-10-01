---
status: completed
spec: [008-bug-build-fix-log-truncated-to-head]
summary: truncateToLines now retains the CI log's tail and prefixes a marker naming the dropped-line count, with an external-package regression-lock suite proving the tail-slice is load-bearing
execution_id: github-update-go-agent-logtail-exec-017-spec-008-gh-cli-log-tail
dark-factory-version: v0.196.0
created: "2026-10-01T06:37:44Z"
queued: "2026-10-01T06:53:31Z"
started: "2026-10-01T06:53:32Z"
completed: "2026-10-01T06:58:30Z"
branch: dark-factory/bug-build-fix-log-truncated-to-head
---

# Retain the log tail and name the dropped count in truncateToLines

<!--
REVIEWER NOTE — not an instruction to the executing agent.
Spec 008 AC7 is a Post-Deploy (Rung-3) operator gate (`kubectlnukeprod -n prod get pods …`
checking the prod build-fix-agent image tag). It is deliberately NOT covered by any generated
prompt: it runs on the host after PR merge + release, outside the YOLO container, and its
`deploy_target` placeholder `v0.18.4` must be set by the operator to the actual shipped tag
once the release cuts (the spec's Phase 0.5 compares it as a literal string, so a stale value
hard-refuses verification). It stays on the spec's Verification ladder. No version is invented here.
-->

<summary>
- The build-fix evidence bound keeps the END of a CI job log instead of its beginning.
- The failing step's own error output — which `gh run view --log-failed` places at the log's tail — therefore reaches the diagnosis model.
- When earlier lines are dropped, the retained evidence opens with a marker naming how many, so a truncated head is distinguishable from a truncated tail.
- A log already within the 200-line bound is returned unchanged and carries no marker.
- The 200-line cap itself is unchanged — a very large log still cannot inflate the diagnosis prompt.
- The regression is locked: reverting the tail-slice back to the head-slice makes the unit suite fail.
- Only the log-bound helper and its test scaffolding change; the `gh` invocation, the single caller, and the 200-line cap are untouched.
- Empty logs and logs shorter than the bound behave exactly as before.
</summary>

<objective>
Fix the build-fix planning evidence bound: `truncateToLines` in `pkg/gh_cli.go` keeps the log's first `n` lines while its own doc comment promises a "diagnosis-sized log tail". Make it retain the final `n` lines and prefix a marker naming the dropped count, so a build-fix diagnosis receives the failing step's error output instead of runner provisioning.
</objective>

<context>
Read `/home/node/.claude/CLAUDE.md` for project conventions — there is no repo-root `CLAUDE.md`.

Read fully before changing anything:

- `pkg/gh_cli.go` — the helper to change sits at the bottom of the file. Current code (verbatim):

```go
// truncateToLines bounds s to at most n lines (diagnosis-sized log tail).
func truncateToLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	return strings.Join(lines[:n], "\n") + "\n... (truncated)"
}
```

  Its only caller is in `FetchFailedLogs`, which ends with `return truncateToLines(string(logOut), 200), nil` — leave that call, and the `gh run view <id> --repo <repo> --log-failed` invocation above it, unchanged.

- `pkg/steps_fix_planning.go` — `runDiagnosis` is the sole caller path. Its evidence selection is (verbatim, around the `logEvidence` block):

```go
	logEvidence := ""
	if s.gh != nil {
		if r, err := s.gh.FetchFailedLogs(ctx, repo, episodeSHA); err == nil {
			logEvidence = r
		} else {
			glog.V(2).Infof("build-fix planning: gh log fetch failed repo=%s err=%v", repo, err)
		}
	}
	if strings.TrimSpace(logEvidence) == "" {
		logEvidence = extractFailingWorkflowLogEvidence(ctx, md)
	}
```

  Note: the body-evidence fallback fires only on EMPTY evidence, never on truncated non-empty evidence. That is out of scope here (see Constraints) — do NOT change this file.

- `pkg/gh_cli_test.go` — `package pkg_test` (external). Existing `Describe` blocks for `prCreateArgs` and `isMissingLabelError`; imports are only Ginkgo, Gomega, and `pkg "github.com/bborbe/github-update-go-agent/pkg"`.
- `pkg/export_test.go` — `package pkg`. The first `var (...)` block is the test-only export surface for `pkg_test`; it already carries `PRCreateArgs = prCreateArgs` and `IsMissingLabelError = isMissingLabelError`.
- `pkg/pkg_suite_test.go` — the suite entrypoint is `func TestSuite(t *testing.T)`.

- Spec: `specs/in-progress/008-bug-build-fix-log-truncated-to-head.md` — the Goal, Desired Behavior (six items), Constraints, and Failure Modes table are the contract. The Failure Modes rows "log shorter than the bound" and "log is empty" must stay correct.

- Coding guides (in-container paths):
  - `/home/node/.claude/plugins/marketplaces/coding/docs/go-testing-guide.md` — Ginkgo/Gomega external-suite conventions.
  - `/home/node/.claude/plugins/marketplaces/coding/docs/go-error-wrapping-guide.md` — error wrapping (not needed on this pure helper, but read for repo idiom).
  - `/home/node/.claude/plugins/marketplaces/coding/docs/changelog-guide.md` — `- <prefix>: <what> [context]`, one bullet per logical change.
  - `/home/node/.claude/plugins/marketplaces/coding/docs/go-precommit.md` — linter limits and formatting.
</context>

<requirements>
1. **Rewrite `truncateToLines` in `pkg/gh_cli.go` to keep the tail and name the dropped count.** Replace the function (and its doc comment) with exactly this shape — the tail-slice expression `lines[len(lines)-n:]` is load-bearing (the spec's own sanity grep looks for it):

```go
// truncateToLines bounds s to at most n lines, keeping the log's TAIL: a CI
// job log's failing step is at the end (`gh run view --log-failed` returns the
// failed job's log in execution order, so provisioning and checkout lead and
// the failing step's output trails). When earlier lines are dropped the result
// is prefixed by a marker naming how many, so a truncated head is
// distinguishable from a truncated tail.
func truncateToLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	dropped := len(lines) - n
	return fmt.Sprintf(
		"... (%d lines dropped)\n%s",
		dropped,
		strings.Join(lines[len(lines)-n:], "\n"),
	)
}
```

   - `fmt` and `strings` are already imported in `pkg/gh_cli.go` — do not add imports.
   - Keep the signature `func truncateToLines(s string, n int) string` unchanged.
   - The within-bound branch stays `return strings.TrimSpace(s)` — an input already within the bound is returned unchanged with NO marker (spec Desired Behavior 3).
   - The marker is exactly one line: `... (<dropped> lines dropped)` followed by `\n`, ahead of the retained tail (spec Desired Behavior 2). For a 649-line input at `n=200`, `dropped` is `449`.

2. **Export the helper for the external test package.** In `pkg/export_test.go`, add `TruncateToLines = truncateToLines` to the first `var (...)` block, next to `PRCreateArgs = prCreateArgs` / `IsMissingLabelError = isMissingLabelError`. This mirrors the repo's existing pattern (every unexported helper the external `pkg_test` package exercises is exported here). Do not add a comment block or restructure the file.

3. **Add the regression-lock suite to `pkg/gh_cli_test.go`.** Extend the imports to include stdlib `"fmt"` and `"strings"` (before the Ginkgo/Gomega group, per goimports-reviser ordering), then append a new `Describe` covering AC1–AC4 plus the empty-input failure mode:

```go
var _ = Describe("truncateToLines", func() {
	// buildLog returns a log of n distinct lines plus the failure text; the line
	// at index failAt (0-based) carries the failure, as `gh run view --log-failed`
	// places it near the tail of the log.
	buildLog := func(n, failAt int) (string, string) {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i)
		}
		failure := "FAIL: 69 folded bullet(s) across 205 released section(s) at v0.66.1."
		if failAt >= 0 && failAt < n {
			lines[failAt] = failure
		}
		return strings.Join(lines, "\n"), failure
	}

	It("keeps the tail, so a failure near the end survives the bound", func() {
		// AC1 regression lock: 649 lines, failure at index 618; at n=200 only the
		// final 200 lines survive, and index 618 is one of them.
		in, failure := buildLog(649, 618)
		Expect(pkg.TruncateToLines(in, 200)).To(ContainSubstring(failure))
	})

	It("names the dropped count ahead of the retained tail", func() {
		// AC2: 649 lines at n=200 drops 449.
		in, _ := buildLog(649, 618)
		Expect(pkg.TruncateToLines(in, 200)).To(ContainSubstring("449"))
	})

	It("returns a log already within the bound unchanged and without a marker", func() {
		// AC3: pre-trimmed input, so the helper's TrimSpace path is identity.
		in, _ := buildLog(50, 49)
		got := pkg.TruncateToLines(in, 200)
		Expect(got).To(Equal(in))
		Expect(got).NotTo(ContainSubstring("dropped"))
	})

	It("stays bounded on a very large log", func() {
		// AC4: 10000 lines at n=200 -> 200 retained lines + 1 marker line.
		in, _ := buildLog(10000, 9999)
		got := pkg.TruncateToLines(in, 200)
		Expect(strings.Count(got, "\n")).To(BeNumerically("<=", 201))
	})

	It("returns an empty log unchanged", func() {
		// Failure mode: empty fetched log -> helper returns empty; the caller's
		// body-evidence fallback (unchanged) handles it.
		Expect(pkg.TruncateToLines("", 200)).To(Equal(""))
	})
})
```

   Do not weaken or remove the existing `prCreateArgs` / `isMissingLabelError` specs.

4. **Record the fix in `CHANGELOG.md`.** The file currently starts at `## v0.18.3`; `## Unreleased` does not exist. Create `## Unreleased` immediately above the `## v0.18.3` heading (do not append to a released section) with exactly one bullet:

```
- fix: keep the CI log's tail (not its head) in the build-fix diagnosis evidence and prefix a marker naming the dropped-line count, so a build-fix planning run on a red build receives the failing step's error output instead of runner provisioning (`truncateToLines` in `pkg/gh_cli.go` documented a "diagnosis-sized log tail" while returning the first 200 lines)
```

   Leave the rest of the changelog untouched.

5. **Before you finish**, re-run every `<verification>` command and confirm each expected result, including the revert-and-restore regression-lock proof (step 2 of `<verification>`), and confirm the file ends with the tail-slice restored (`grep -n 'lines\[len(lines)-n:\]' pkg/gh_cli.go` returns exactly one match). Confirm you changed no file other than `pkg/gh_cli.go`, `pkg/export_test.go`, `pkg/gh_cli_test.go`, and `CHANGELOG.md`.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git.
- `truncateToLines` keeps its single caller and its signature; the production code change is confined to `pkg/gh_cli.go`. The only other files touched are test scaffolding (`pkg/export_test.go`, `pkg/gh_cli_test.go`) and `CHANGELOG.md`.
- The bound stays at 200 lines — do NOT change the cap's size. `FetchFailedLogs` keeps `truncateToLines(string(logOut), 200)`.
- `runDiagnosis`'s body-evidence fallback (`pkg/steps_fix_planning.go`) keeps firing only on EMPTY gh output. Widening it to also fire on truncated output is out of scope — do NOT touch `pkg/steps_fix_planning.go`.
- No change to the `gh` invocation itself (`gh run view <id> --repo <repo> --log-failed`).
- The repo's gate (`make precommit`) and existing suites stay green.
- Repo conventions: BSD license header, `github.com/bborbe/errors` wrapping (no `fmt.Errorf`), `glog` logging, `gofmt`/`goimports`/`golines` (max-len 100) clean.
- Container-autonomous: file edits + `make`/`go` only. No `kubectl`, no `docker`, no `gh`, no PR/deploy steps. Do NOT run `git` commands — this container's `.git` is masked.
- Do NOT add retry logic, backoff, max-attempts, config fields, or flags.
</constraints>

<verification>
Run in `/workspace`:

1. **The new suite (AC1–AC4 + empty input):**
```bash
go test ./pkg/... -run TestSuite -count=1
# expect: ok
```

2. **Prove the lock locks (AC1) — revert, observe failure, restore:**
```bash
cp pkg/gh_cli.go /tmp/gh_cli.go.bak
trap 'cp /tmp/gh_cli.go.bak pkg/gh_cli.go' EXIT   # restore even if this run is killed mid-step
sed -i 's/lines\[len(lines)-n:\]/lines[:n]/' pkg/gh_cli.go
go test ./pkg/... -run TestSuite -count=1; echo "reverted exit=$?"
# expect: non-zero exit — the head-slice drops the failing line and the marker
cp /tmp/gh_cli.go.bak pkg/gh_cli.go
go test ./pkg/... -run TestSuite -count=1; echo "restored exit=$?"
# expect: exit 0
```
The revert-and-restore is the spec's AC1 evidence ("both runs recorded in the PR"); report both exit codes in your completion report. The file MUST end restored.

3. **Secondary sanity — the tail-slice is the implementation:**
```bash
grep -n 'lines\[len(lines)-n:\]' pkg/gh_cli.go
# expect: exactly 1 match (the restored tail-slice)
```

4. **Full gate (AC5):**
```bash
make precommit
# expect: exit 0
```
</verification>
