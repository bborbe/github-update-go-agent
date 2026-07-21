// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import "github.com/bborbe/github-update-go-agent/pkg/git"

// ResultOutput is the typed contract for the `## Result` JSON section the
// execution step writes. Round-trips with agentlib.MarshalSectionTyped +
// agentlib.ExtractSection[ResultOutput].
//
// Shapes (design § 4.3):
//   - Outcome="opened"  — draft PR created; Branch/PRURL populated, GateExit=0
//   - Outcome="adopted" — crash-window replay found the PR already open; adopted as-is
//   - Outcome="failed"  — any failure; ErrorCategory + Error populated
//
// Invariant (design § 4.4): Branch == "fix/update-go-" + ref[:7];
// VulnsFixed ⊆ {v.ID | v ∈ Plan.Vulns, Action=fix}.
type ResultOutput struct {
	Outcome string `json:"outcome"`
	Branch  string `json:"branch,omitempty"`
	PRURL   string `json:"pr_url,omitempty"`

	// GateExit is the exit code of the last gate target run (0 = green).
	// omitempty is deliberately NOT applied — an explicit 0 documents the
	// green gate.
	GateExit int `json:"gate_exit"`

	// DepsUpdated counts the module bumps the update sequence applied.
	DepsUpdated int `json:"deps_updated"`

	// VulnsFixed lists the finding IDs resolved by the update — always a
	// subset of the plan's fix-action vulns.
	VulnsFixed []string `json:"vulns_fixed,omitempty"`

	// FailedTarget names the gate target that stayed red (failure path only).
	FailedTarget string `json:"failed_target,omitempty"`

	ErrorCategory git.ErrorCategory `json:"error_category,omitempty"`
	Error         string            `json:"error,omitempty"`
}

// Outcome values for ResultOutput.Outcome.
const (
	ResultOutcomeOpened  = "opened"
	ResultOutcomeAdopted = "adopted"
	ResultOutcomeFailed  = "failed"
)
