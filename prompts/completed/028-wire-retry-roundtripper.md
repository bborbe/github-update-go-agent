---
status: completed
created: "2026-11-28T09:34:13Z"
queued: "2026-11-28T09:34:13Z"
completed: "2026-11-28T09:43:01Z"
pr-url: https://github.com/bborbe/github-update-go-agent/pull/127
---

<summary>
- Every GitHub HTTP client this agent builds now retries a rate-limit response instead of returning the error
- A 429 or a rate-limiting 403 from api.github.com is retried with the wait the response asks for, up to five times
- A wait longer than one second is deferred, so the caller sees the real rate-limit response promptly instead of hanging until the client timeout
- Each retry emits one log line naming the attempt, the status, the class and the wait
- Two clients in this repo are not GitHub and stay untouched
</summary>

<objective>
Route this agent's four GitHub HTTP clients through the shared rate-limit-aware retry transport, so a throttled response delays the agent instead of ending its work item.
</objective>

<context>
Read CLAUDE.md for project conventions.

Four GitHub clients are built in `pkg/`, each as a bare `&http.Client{...}`:
- `pkg/fleet_source.go` — `newFleetHTTPClient(token, apiBase)` (~line 101), client literal at ~103, `Timeout: fleetRequestTimeout` (15s), `apiBase` defaulting to `fleetAPIBase` = `https://api.github.com`.
- `pkg/maintainerconfig/fetcher.go` — `NewHTTPFetcher(token)` (~line 89), client literal at ~91, `Timeout: fetchTimeout` (15s), `apiBase: "https://api.github.com"`; and its test seam `newHTTPFetcherWithBase(token, apiBase)` (~line 99), literal at ~101, `apiBase: apiBase`.
- `pkg/steps_gh_token.go` — `newGHTokenCheckStep(ghToken, httpURL)` (~line 56), client literal at ~60, `Timeout: 10 * time.Second`, probing `rateLimitURL` = `https://api.github.com/rate_limit`.

**Not GitHub — leave unwrapped:** `pkg/target_go.go:32` (`goReleaseIndexClient`, go.dev) and `pkg/osv_fetcher.go:60` (OSV). Say so in the completion report so the every-site check is satisfied by justification rather than omission.

`github.com/bborbe/http` is pinned at v1.26.33 as an indirect dependency. Its `NewRoundTripperRetryWithBackoff(roundTripper, RetryBackoffOptions)` returns a `http.RoundTripper` that retries a 403/429 from the hosts in `RetryHosts`, sleeping the `Retry-After` duration (and the `X-RateLimit-Reset` delta for primary exhaustion), up to `MaxAttempts` (default 5), emitting `retry attempt=<n> status=<status> class=<class> wait=<duration>` per attempt.

⚠️ **`MaxWait` bounds one wait, not the call.** `MaxAttempts` multiplies it, so `MaxWait: 5s` would mean 25 seconds of sleeping inside a 15-second client — which still ends in `context deadline exceeded`. Use `MaxWait: 1 * time.Second`: 5 × 1s = 5s, inside the 10-second and 15-second timeouts with room for the requests themselves.

This repository has no `vendor/` directory, so the dependency bump needs `go mod tidy` and nothing else.
</context>

<requirements>
1. In `pkg/fleet_source.go`, add one unexported helper:

   ```go
   // retryTransportFor wraps the default transport with the shared rate-limit
   // retry round tripper. api.github.com is always retried; the host of apiBase
   // is added too when it differs, so a test seam pointing at a stub server is
   // covered. MaxWait bounds one wait, not the call: MaxAttempts (default 5)
   // multiplies it, so one second keeps the worst case at five seconds, inside
   // the client timeouts.
   func retryTransportFor(apiBase string) http.RoundTripper {
       hosts := []string{"api.github.com"}
       if u, err := url.Parse(apiBase); err == nil && u.Hostname() != "" && u.Hostname() != "api.github.com" {
           hosts = append(hosts, u.Hostname())
       }
       return libhttp.NewRoundTripperRetryWithBackoff(
           http.DefaultTransport,
           libhttp.RetryBackoffOptions{RetryHosts: hosts, MaxWait: 1 * time.Second},
       )
   }
   ```

   Import it as `libhttp "github.com/bborbe/http"`, adding `net/url` / `time` if missing. **Two copies exist, not three:** `pkg/steps_gh_token.go` is in **package `pkg`**, the same package as `pkg/fleet_source.go`, so it reuses that helper — do not declare a second one there (it will not compile). `pkg/maintainerconfig` is a separate package and needs its own copy. The two must be byte-identical apart from the doc comment; do not export either.

2. Wire all four client literals with `Transport: retryTransportFor(<apiBase>)`:
   - `pkg/fleet_source.go` `newFleetHTTPClient` → `retryTransportFor(apiBase)`
   - `pkg/maintainerconfig/fetcher.go` `NewHTTPFetcher` → `retryTransportFor("https://api.github.com")`
   - `pkg/maintainerconfig/fetcher.go` `newHTTPFetcherWithBase` → `retryTransportFor(apiBase)`
   - `pkg/steps_gh_token.go` `newGHTokenCheckStep` → `retryTransportFor(httpURL)`

3. Do **not** wire `pkg/target_go.go` or `pkg/osv_fetcher.go` — neither talks to GitHub. State that explicitly in your completion report.

4. Do not change request building, pagination, header sets, error wording, sentinel errors (`ErrFileNotFound`), the `gh` CLI path (`pkg/gh_cli.go`), or any exported signature. The `gh` subprocess path cannot be wrapped by a Go round-tripper and is out of scope for this change.

5. Raise the dependency to the released tag: `go get github.com/bborbe/http@v1.28.1` then `go mod tidy`. **v1.28.1 is required** — v1.28.0 mislabels a 429 carrying positive `X-RateLimit-Remaining` with no `Retry-After` as `unclassified`, and that is the label the retry line carries. Allow the fetch if it is not cached. Confirm `go.mod` lists `github.com/bborbe/http v1.28.1` in the direct `require` block, and that any `bborbe/*` transitive bumps it forces are kept (they are required by that version, not gratuitous).

6. Add tests using each package's existing test seams. Where a package exposes a `*WithBase` constructor or an `export_test.go` alias, use it; where it does not, use the package-private constructor from an in-package test file. Per wired package, cover:
   a. **Retry fires.** Stub returns `429` with `Retry-After: 1` on the first request and a valid `200` on the second; assert **exactly two requests** reached the server and the call succeeded.
   b. **Total budget holds.** Stub returns `429` with **`Retry-After: 4`** on every request; assert the error text contains **`status 429`** for the fleet client (`fleetCheckStatus` wraps `ErrFleetRateLimited` with `"%s: status %d"`) and for the maintainerconfig fetcher (`"fetch .maintainer.yaml: status %d …"`), and **`HTTP 429`** for the token-check step (`"GH rate_limit returned HTTP %d"`). The three packages do not share one wording — asserting a single literal fails two of them. Also assert the text does **not** contain `context deadline exceeded`, that **exactly one** request reached the server, and that elapsed time is under five seconds. Four seconds is the discriminating value: at a five-second bound it is slept five times (20s, past the timeout); at a one-second bound it defers immediately.

   Match the surrounding Ginkgo v2 / Gomega style.

7. Update `CHANGELOG.md` with one `## Unreleased` bullet describing the retry wiring and the one-second deferral bound. If no `## Unreleased` section exists, create one directly above the newest released `## vX.Y.Z` heading; do not rename or renumber any released heading.

8. Before finishing, re-run `<verification>` and confirm it passes; then walk each numbered requirement against the change and report any that is unmet, including which `http.Client{` sites you left unwrapped and why.
</requirements>

<constraints>
- Do NOT commit — dark-factory handles git
- Existing tests must pass unchanged
- Do not touch `pkg/gh_cli.go`, `pkg/target_go.go`, `pkg/osv_fetcher.go`, or any file outside the three packages named above plus `go.mod`, `go.sum` and `CHANGELOG.md`
- Do not raise or remove the client `Timeout` values — the bound belongs on the retry wait
- Add no dependency beyond the `bborbe/http` bump and what it forces
</constraints>

<verification>
Run `ROOTDIR=/workspace make precommit` -- must pass.
</verification>
