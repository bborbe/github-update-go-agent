// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// PlanOutput is the typed contract for the `## Plan` JSON section the
// planning step writes for every github-update-go task. Round-trips with
// agentlib.MarshalSectionTyped + agentlib.ExtractSection[PlanOutput].
//
// Shapes (design § 4.3):
//   - Outcome="ready"            — update work exists; GateTargets populated, HasWork=true
//   - Outcome="no_update_needed" — repo already current; HasWork=false
//   - Outcome="needs_input"      — park path (unfixable finding / precondition); Reason populated
//
// No `Details map[string]any`: concrete fields only. Future fields require
// a design amendment.
type PlanOutput struct {
	Outcome string `json:"outcome"`

	// HasWork is true when the execution phase has anything to do.
	// omitempty is deliberately NOT applied so a `false` decision is
	// always written explicitly.
	HasWork bool `json:"has_work"`

	// GoBump records the go-directive bump (from current directive to the
	// image toolchain per design D5). Nil when the directive is already
	// current.
	GoBump *GoBump `json:"go_bump,omitempty"`

	// DepUpdatesExpected is true when `go list -u -m` style inspection
	// found outdated direct dependencies.
	DepUpdatesExpected bool `json:"dep_updates_expected"`

	// GateTargets are the Makefile targets forming the repo's own gate
	// (e.g. precommit, check, vulncheck). Execution and ai_review re-run
	// exactly these to exit 0 — never a hardcoded scanner.
	GateTargets []string `json:"gate_targets,omitempty"`

	// Vulns holds every scanner finding with its fix-vs-park classification.
	Vulns []PlanVuln `json:"vulns,omitempty"`

	// Reason carries the human-readable explanation on the
	// needs_input / no_update_needed paths.
	Reason string `json:"reason,omitempty"`
}

// GoBump records a go-directive version bump.
type GoBump struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// PlanVuln is one scanner finding with its planned action.
type PlanVuln struct {
	// ID is the finding identifier (GO-*, CVE-*, GHSA-*).
	ID string `json:"id"`
	// Package is the vulnerable module path.
	Package string `json:"package,omitempty"`
	// FixedVersion is the patched version the scanner reported; empty when
	// no fix exists (the park case).
	FixedVersion string `json:"fixed_version,omitempty"`
	// Scanner names which gate scanner flagged the finding
	// (govulncheck | osv-scanner | trivy).
	Scanner string `json:"scanner,omitempty"`
	// Action is "fix" (patched version exists — bump it) or "park"
	// (no fix / out-of-scope major — task parks per design D4).
	Action string `json:"action"`
	// Reason is the one-line classification justification.
	Reason string `json:"reason,omitempty"`
}

// Outcome values for PlanOutput.Outcome.
const (
	PlanOutcomeReady          = "ready"
	PlanOutcomeNoUpdateNeeded = "no_update_needed"
	PlanOutcomeNeedsInput     = "needs_input"
)

// Action values for PlanVuln.Action.
const (
	VulnActionFix  = "fix"
	VulnActionPark = "park"
)
