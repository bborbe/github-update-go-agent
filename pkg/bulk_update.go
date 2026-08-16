// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// bulkUpdateTimeout bounds each command in the bulk-update sequence. Generous
// relative to a real run (`go get -u ./...` on the largest bborbe repos
// measured well under 5 minutes) but hard — a command that exceeds it is
// reported as a failure, never retried.
const bulkUpdateTimeout = 8 * time.Minute

// outputTailBytes caps how much command output is carried into the prompt.
const outputTailBytes = 2000

// BulkUpdateResult reports what the deterministic bulk update did.
//
// Like FunnelResult in github-pr-review-agent, this is returned instead of a
// bare error for the expected non-fatal outcomes, so the caller can surface a
// fail-closed condition into the prompt rather than aborting the pipeline. The
// model must be told the update did not run — never left to assume it did.
type BulkUpdateResult struct {
	// Ran is false when the sequence could not complete. FailDetail then
	// explains why, and the model is told to treat the update as NOT done.
	Ran bool
	// FailDetail is empty when Ran is true.
	FailDetail string
	// Output is the tail of the combined command output, for the prompt.
	Output string
}

//counterfeiter:generate -o ../mocks/bulk_updater.go --fake-name BulkUpdater . BulkUpdater

// BulkUpdater runs the mechanical dependency-update sequence
// (`go get -u ./...` + `go mod tidy`) in Go, before the execution model call.
//
// The agent runs this itself rather than instructing the model to. Left to the
// model, a long-running `go get` invites backgrounding: on 2026-08-16 the
// execution model put it in a harness background task and then blocked on
// TaskOutput for 600s, timed out, and re-issued the identical blocking call on
// the same task_id — burning the Job's full 1800s activeDeadlineSeconds and
// producing nothing (bborbe/ip, job bc3c6599; also bborbe/run, bborbe/beactive).
//
// Prompt rules could not prevent it: execution.md already forbade backgrounding,
// but named only the shell forms (`&`, `nohup`, detached jobs), not the
// harness's own run_in_background/TaskOutput. Running the step in Go removes
// the model's ability to express it in any form at all — the same reasoning
// that moved the ast-grep funnel into Go in github-pr-review-agent.
type BulkUpdater interface {
	Run(ctx context.Context, workdir string) (BulkUpdateResult, error)
}

type bulkUpdater struct{}

// NewBulkUpdater constructs the production BulkUpdater.
func NewBulkUpdater() BulkUpdater {
	return &bulkUpdater{}
}

// Run executes `go get -u ./...` then `go mod tidy` in workdir, each under a
// hard timeout. A non-nil error is reserved for unexpected Go-level failures;
// an expected tooling failure comes back as Ran=false with FailDetail so the
// caller can put a fail-closed note in the prompt.
func (b *bulkUpdater) Run(ctx context.Context, workdir string) (BulkUpdateResult, error) {
	var combined strings.Builder
	for _, args := range [][]string{
		{"get", "-u", "./..."},
		{"mod", "tidy"},
	} {
		out, err := b.run(ctx, workdir, args)
		combined.WriteString("$ go ")
		combined.WriteString(strings.Join(args, " "))
		combined.WriteString("\n")
		combined.WriteString(out)
		combined.WriteString("\n")
		if err != nil {
			glog.V(1).Infof(
				"event=bulk_update_failed workdir=%s cmd=%q err=%v",
				workdir, strings.Join(args, " "), err,
			)
			return BulkUpdateResult{
				Ran:        false,
				FailDetail: "go " + strings.Join(args, " ") + " failed: " + err.Error(),
				Output:     tail(combined.String()),
			}, nil
		}
	}
	glog.V(1).Infof("event=bulk_update_ok workdir=%s", workdir)
	return BulkUpdateResult{Ran: true, Output: tail(combined.String())}, nil
}

// run executes one `go` invocation under bulkUpdateTimeout. The timeout is the
// point of this function: it is what makes the step bounded instead of a retry
// loop, so a hung toolchain fails loudly rather than consuming the Job budget.
func (b *bulkUpdater) run(ctx context.Context, workdir string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, bulkUpdateTimeout)
	defer cancel()

	full := append([]string{"-C", workdir}, args...)
	// #nosec G204 -- args are fixed literals above; workdir is agent-controlled.
	cmd := exec.CommandContext(ctx, "go", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return string(out), errors.Wrapf(
				ctx, ctx.Err(), "go %s exceeded %s", strings.Join(args, " "), bulkUpdateTimeout,
			)
		}
		return string(out), errors.Wrapf(ctx, err, "go %s", strings.Join(args, " "))
	}
	return string(out), nil
}

// tail returns the last outputTailBytes of s, prefixed to make truncation obvious.
func tail(s string) string {
	if len(s) <= outputTailBytes {
		return s
	}
	return "…(truncated)…\n" + s[len(s)-outputTailBytes:]
}
