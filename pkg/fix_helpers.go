// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// readFixRequired reads the build-fix task's required frontmatter fields.
// Returns the first missing field name ("" when all present) plus the
// resolved repo and episode SHA.
func readFixRequired(md *agentlib.Markdown) (missing, repo, episodeSHA string) {
	for _, field := range fixRequiredFrontmatterFields {
		v, _ := md.Frontmatter.String(field)
		if strings.TrimSpace(v) == "" {
			return field, "", ""
		}
	}
	repo, _ = md.Frontmatter.String("repo")
	episodeSHA, _ = md.Frontmatter.String("episode_sha")
	return "", strings.TrimSpace(repo), strings.TrimSpace(episodeSHA)
}

// setupFixWorkdir creates (or re-creates) the ephemeral clone dir for a
// build-fix task, keyed by the task's deterministic identifier so a re-run
// lands on the same path.
func setupFixWorkdir(md *agentlib.Markdown) string {
	taskID := md.Frontmatter["task_identifier"]
	id := "unknown"
	if s, ok := taskID.(string); ok && s != "" {
		id = s
	}
	dir := filepath.Join(os.TempDir(), fixWorkdirPrefix+id)
	if err := os.RemoveAll(dir); err != nil {
		glog.Warningf("build-fix: remove stale workdir failed: path=%s err=%v", dir, err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		glog.Warningf("build-fix: create workdir failed: path=%s err=%v", dir, err)
	}
	return dir
}

// extractFailingWorkflowLogEvidence pulls the failing-workflow + log lines
// from the task body so the diagnosis has concrete evidence without an extra
// gh round-trip. Returns "" when the body carries neither.
func extractFailingWorkflowLogEvidence(md *agentlib.Markdown) string {
	if len(md.Sections) == 0 {
		return ""
	}
	// Keep the ## Failing Workflows table (run URLs + job names) plus any
	// ## Log section verbatim — those are the reproduction evidence.
	var out []string
	for _, sec := range md.Sections {
		switch sec.Heading {
		case "## Failing Workflows", "## Log":
			out = append(out, strings.TrimSpace(sec.Body))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "\n\n")
}

// parseFixPlan unmarshals and validates the diagnosis LLM's ## Fix Plan JSON
// (the runner returns the marshaled FixPlanOutput in Output). A verdict
// outside the four known values is an error — a fabricated verdict must fail
// loud rather than route to an arbitrary phase.
func parseFixPlan(ctx context.Context, raw string) (*FixPlanOutput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.Wrap(
			ctx,
			errors.New(ctx, "empty diagnosis output"),
			"parse build-fix plan",
		)
	}
	var plan FixPlanOutput
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return nil, errors.Wrap(ctx, err, "unmarshal build-fix plan")
	}
	valid := false
	for _, v := range AvailableFixVerdicts {
		if plan.Verdict == v {
			valid = true
			break
		}
	}
	if !valid {
		return nil, errors.Wrap(
			ctx,
			errors.New(ctx, "unknown verdict "+string(plan.Verdict)),
			"invalid build-fix verdict",
		)
	}
	return &plan, nil
}

// writeFixPlanSection replaces the ## Fix Plan section with the typed JSON.
func writeFixPlanSection(ctx context.Context, md *agentlib.Markdown, plan *FixPlanOutput) error {
	section, err := agentlib.MarshalSectionTyped(ctx, "## Fix Plan", *plan)
	if err != nil {
		return errors.Wrap(ctx, err, "marshal ## Fix Plan section")
	}
	md.ReplaceSection(section)
	return nil
}

// extractFixPlan reads the ## Fix Plan section written by planning.
func extractFixPlan(ctx context.Context, md *agentlib.Markdown) (*FixPlanOutput, error) {
	return agentlib.ExtractSection[FixPlanOutput](ctx, md, "## Fix Plan")
}
