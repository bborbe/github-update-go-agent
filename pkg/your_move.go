// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"net/url"
	"strconv"
	"strings"

	agentlib "github.com/bborbe/agent"
)

// yourMoveHeading is the fixed heading of the operator-action block a task
// routed to human_review opens with (named in the spec verification:
// grep -n "^## Your Move").
const yourMoveHeading = "## Your Move"

// isValidPRURL reports whether raw is an absolute http(s) URL — the only
// shape safe to interpolate into the block as a clickable markdown link.
func isValidPRURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// buildYourMoveBody renders the plain-text operator-action block for a
// human_review handoff: a clickable PR link, the single action the operator
// owns (merge), and what the change consists of. No JSON — the
// machine-readable contract stays in ## Plan / ## Result / ## Review. An
// empty or non-http(s) PRURL renders the "PR URL unavailable" placeholder
// (the ## Result JSON still carries the branch name); the Go version bump
// is shown when go_bump is present, otherwise the dependency-update count
// and the fixed-vulnerability IDs, and an explicit "No version bump
// recorded" line when none were recorded.
func buildYourMoveBody(result *ResultOutput, plan *PlanOutput) string {
	lines := []string{}
	if isValidPRURL(result.PRURL) {
		lines = append(lines, "[Open the PR]("+result.PRURL+")")
	} else {
		lines = append(lines, "PR URL unavailable")
	}
	lines = append(lines, "**Merge the PR** to apply the update.")
	lines = append(lines, changeSummaryLines(result, plan)...)
	return strings.Join(lines, "\n\n")
}

// changeSummaryLines renders the plain-text change summary: the Go version
// bump when the plan records one, otherwise the dependency-update count and
// the fixed-vulnerability IDs, and an explicit "No version bump recorded"
// line when none were recorded.
func changeSummaryLines(result *ResultOutput, plan *PlanOutput) []string {
	if plan.GoBump != nil {
		return []string{"Go version bump: " + plan.GoBump.From + " → " + plan.GoBump.To}
	}
	lines := []string{}
	if result.DepsUpdated > 0 {
		lines = append(lines, "Updated "+strconv.Itoa(result.DepsUpdated)+" dependencies")
	}
	if len(result.VulnsFixed) > 0 {
		lines = append(lines, "Fixed vulnerabilities: "+strings.Join(result.VulnsFixed, ", "))
	}
	if result.DepsUpdated == 0 && len(result.VulnsFixed) == 0 {
		lines = append(lines, "No version bump recorded")
	}
	return lines
}

// writeYourMoveSection inserts the ## Your Move block immediately above
// ## Plan, or updates it in place on a re-run so a re-triggered review
// never duplicates it. Only called when the review routes to human_review.
func writeYourMoveSection(md *agentlib.Markdown, result *ResultOutput, plan *PlanOutput) {
	section := agentlib.Section{Heading: yourMoveHeading, Body: buildYourMoveBody(result, plan)}
	if existing, ok := md.FindSection(yourMoveHeading); ok {
		existing.Body = section.Body
		return
	}
	pos := len(md.Sections)
	for i, s := range md.Sections {
		if s.Heading == "## Plan" {
			pos = i
			break
		}
	}
	md.InsertSection(pos, section)
}
