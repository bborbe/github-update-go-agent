// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// ReviewChecks holds the boolean verification results of the pure-Go
// ai_review step. Every field is derived from independent re-execution —
// never copied from ## Result (design § 4.4).
type ReviewChecks struct {
	// PROpen — `gh pr view` reports state OPEN.
	PROpen bool `json:"pr_open"`
	// PRDraft — `gh pr view` reports isDraft true (the agent never readies).
	PRDraft bool `json:"pr_draft"`
	// GateGreen — every planned gate target re-ran to exit 0 on a fresh
	// worktree at the branch.
	GateGreen bool `json:"gate_green"`
	// VulnsClear — the scanner-bearing gate targets came back green on the
	// re-run (the gate IS the vuln verdict, per design § 7.3 the repo's
	// gate wraps govulncheck/osv-scanner/trivy).
	VulnsClear bool `json:"vulns_clear"`
	// ChangelogUnreleased — CHANGELOG.md carries a bullet under
	// ## Unreleased and introduces no new version header vs master.
	ChangelogUnreleased bool `json:"changelog_unreleased"`
	// NoNewTag — `git ls-remote --tags` shows no tag pointing at any
	// branch commit.
	NoNewTag bool `json:"no_new_tag"`
}

// ReviewOutput is the typed contract for the `## Review` JSON section the
// ai_review step writes. Round-trips with agentlib.MarshalSectionTyped +
// agentlib.ExtractSection[ReviewOutput].
type ReviewOutput struct {
	Approved bool         `json:"approved"`
	Checks   ReviewChecks `json:"checks"`
	Notes    string       `json:"notes"`
}
