// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// FixPlanOutput is the typed contract for the `## Fix Plan` JSON section the
// build-fixer's planning step writes for every build-fix task. Round-trips
// with agentlib.MarshalSectionTyped + agentlib.ExtractSection[FixPlanOutput].
//
// Verdicts (the four-way diagnosis, design § 2026-08-22):
//   - "no_fix_needed" — build already green at HEAD; task closes as success
//   - "chain_update"  — root cause is a stale dependency / vulnerability;
//     execution emits a github-update-go task (chain to the updater agent)
//   - "file_spec"     — root cause is a code / test bug; execution files a
//     kind:bug spec for dark-factory to fix
//   - "needs_input"   — ambiguous / insufficient log evidence; escalate to
//     the operator inbox
//
// No `Details map[string]any`: concrete fields only. Future fields require
// a design amendment.
type FixPlanOutput struct {
	Verdict string `json:"verdict"`

	// Reason carries the human-readable diagnosis on every path — the
	// reproduction/expected-actual summary the spec writer or escalation
	// message needs.
	Reason string `json:"reason,omitempty"`

	// FailingWorkflows are the workflow names observed red in the task body
	// (or discovered during diagnosis). The spec writer cites them verbatim.
	FailingWorkflows []string `json:"failing_workflows,omitempty"`

	// EpisodeSHA is the CI episode SHA under diagnosis, echoed back for the
	// spec / chain task to pin.
	EpisodeSHA string `json:"episode_sha,omitempty"`
}

// Fix verdict values for FixPlanOutput.Verdict.
const (
	FixVerdictNoFixNeeded = "no_fix_needed"
	FixVerdictChainUpdate = "chain_update"
	FixVerdictFileSpec    = "file_spec"
	FixVerdictNeedsInput  = "needs_input"
)
