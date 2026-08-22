// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"strings"
)

// BuildBugSpec renders the kind:bug spec dark-factory consumes, per
// dark-factory/docs/bug-workflow.md: frontmatter status: idea + kind: bug,
// then Reproduction / Expected vs Actual / Why this is a bug. Deterministic
// from the diagnosis plan + failing-workflow evidence — no LLM, so the spec
// cannot drift from what planning classified.
func BuildBugSpec(repo string, plan *FixPlanOutput, logEvidence string) string {
	workflows := strings.Join(plan.FailingWorkflows, ", ")
	if workflows == "" {
		workflows = "(see failing-workflow evidence below)"
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("status: idea\n")
	b.WriteString("kind: bug\n")
	b.WriteString("---\n\n")
	b.WriteString("# Build Failure: " + repo + "\n\n")
	b.WriteString(
		"Filed automatically by the build-fix agent for the CI episode `" + plan.EpisodeSHA + "`.\n\n",
	)
	b.WriteString("## Summary\n\n")
	b.WriteString(
		"The default-branch build for `" + repo + "` is failing; the build-fix diagnosis classified this as a code/test bug (verdict `file_spec`).\n\n",
	)
	b.WriteString("## Reproduction\n\n")
	b.WriteString("Failing workflow(s): " + workflows + "\n\n")
	b.WriteString("Episode SHA: `" + plan.EpisodeSHA + "`\n\n")
	if strings.TrimSpace(logEvidence) != "" {
		b.WriteString("Log evidence:\n\n```text\n")
		b.WriteString(strings.TrimSpace(logEvidence))
		b.WriteString("\n```\n\n")
	} else {
		b.WriteString("(no log evidence captured at filing time)\n\n")
	}
	b.WriteString("## Expected vs Actual\n\n")
	b.WriteString("**Expected:** green CI on the default branch.\n")
	b.WriteString("**Actual:** `" + plan.Reason + "`\n\n")
	b.WriteString("## Why this is a bug\n\n")
	b.WriteString(
		"The default-branch build is the repository's quality gate; a red build blocks merges. Diagnosis: `" + plan.Reason + "`\n",
	)
	return b.String()
}

// sanitizeSlug reduces an arbitrary string to [a-z0-9-] for filename safety.
func sanitizeSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '/' || r == ':':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "workflow"
	}
	return out
}
