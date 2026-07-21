// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	"github.com/golang/glog"

	"github.com/bborbe/github-update-go-agent/pkg/git"
	"github.com/bborbe/github-update-go-agent/pkg/prompts"
)

// AgentLogin is the GitHub-task-system identity of this agent. Per the
// platform doctrine the CONTROLLER writes previous_assignee — the constant
// exists for logs and messages only; steps never mutate assignee.
const AgentLogin = "github-update-go-agent"

// workdirPrefix roots every ephemeral clone under os.TempDir().
const workdirPrefix = "github-update-go-"

// requiredFrontmatterFields are the keys read from the task's frontmatter
// before the step does any IO. Missing OR empty → needs_input naming the
// field (message only — the controller owns the escalation envelope).
//
// Order matters for deterministic error messages: first missing field wins.
var requiredFrontmatterFields = []string{
	"repo",
	"clone_url",
	"ref",
}

// suppressionSurfacesHint names the three fleet-convention ignore surfaces an
// operator-approved suppression would touch (design § Suppression surfaces).
// The agent READS these (the gate already respects them) but NEVER writes
// them — parking is the only agent-side move on an unfixable finding.
const suppressionSurfacesHint = "an operator-approved suppression would touch: " +
	"Makefile.precommit (VULNCHECK_IGNORE, may live in Makefile), " +
	".osv-scanner.toml ([[IgnoredVulns]] id + reason), " +
	".trivyignore (one id per line + # reason) — " +
	"see [[Exclude a No-Fix Vulnerability Across the Fleet]] (add-vuln-ignore.sh), " +
	"then re-delegate"

// planningStep implements agentlib.Step for the planning phase: clone at
// ref, run the Claude inspection call, classify, park-or-advance.
type planningStep struct {
	runner  claudelib.ClaudeRunner
	ops     git.GitOps
	ghToken string
	scope   InstallationScope
}

// NewPlanningStep wires the planning step with its Claude runner (inspection
// LLM), the GitOps seam (clone at ref), the GitHub token (HTTPS auth URL
// transformation), and the installation-scope allowlist check.
func NewPlanningStep(
	runner claudelib.ClaudeRunner,
	ops git.GitOps,
	ghToken string,
	scope InstallationScope,
) agentlib.Step {
	return &planningStep{runner: runner, ops: ops, ghToken: ghToken, scope: scope}
}

// Name implements agentlib.Step.
func (s *planningStep) Name() string { return "github-update-go-plan" }

// ShouldRun always returns true — planning is idempotent: a re-trigger
// re-clones, re-scans, and replaces the existing ## Plan section in place
// (the operator suppress-then-re-delegate loop depends on a fresh scan).
func (s *planningStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run executes the planning pipeline:
//  1. Required-frontmatter validation → NeedsInput (message only; the step
//     NEVER writes ## Failure and NEVER mutates assignee/status — the
//     controller owns the escalation envelope).
//  2. Clone at ref via GitOps → Failed on clone/auth error.
//  3. Claude inspection call with the planning prompt → parse PlanOutput.
//  4. Any park-action finding → ## Plan + NeedsInput naming finding IDs,
//     scanners, and the three suppression surfaces (design D4).
//  5. no_update_needed → ## Plan + Done/NextPhase done (task completes).
//  6. ready → ## Plan + Done/NextPhase execution.
func (s *planningStep) Run(ctx context.Context, md *agentlib.Markdown) (*agentlib.Result, error) {
	missingField, repo, cloneURL, ref := readRequired(md)
	if missingField != "" {
		glog.V(2).Infof("planning: missing frontmatter field=%s — escalating", missingField)
		return needsInput("required frontmatter field missing: " + missingField), nil
	}

	// Allowlist preflight (F9): the App installation's repository selection is
	// the per-stage allowlist. A repo outside it parks here — before clone and
	// the full update run — instead of failing at push ten minutes later.
	if s.scope.Allows(ctx, repo) == ScopeDenied {
		glog.V(2).Infof("planning: repo %s not in App installation — parking", repo)
		return needsInput("repo " + repo + " is not in the GitHub App installation's " +
			"repository list (per-stage allowlist) — add it to the installation or " +
			"route the task to a stage whose App covers it"), nil
	}

	workdir := setupWorkdir(md, repo)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf("planning: workdir cleanup failed: path=%s err=%v", workdir, err)
		}
	}()

	authedURL := injectToken(normalizeCloneURLToHTTPS(cloneURL), s.ghToken)
	if err := s.ops.CloneAtRef(ctx, authedURL, ref, workdir); err != nil {
		return s.failClone(repo, err), nil
	}

	plan, failResult := s.runInspection(ctx, md, workdir)
	if failResult != nil {
		return failResult, nil
	}

	if parked := parkFindings(plan); len(parked) > 0 {
		if err := writePlanSection(ctx, md, plan); err != nil {
			return nil, err
		}
		msg := parkMessage(parked)
		glog.V(2).Infof("planning: parking task — %s", msg)
		return needsInput(msg), nil
	}

	if plan.Outcome == PlanOutcomeNeedsInput {
		if err := writePlanSection(ctx, md, plan); err != nil {
			return nil, err
		}
		return needsInput(plan.Reason), nil
	}

	if err := writePlanSection(ctx, md, plan); err != nil {
		return nil, err
	}

	if plan.Outcome == PlanOutcomeNoUpdateNeeded || !plan.HasWork {
		glog.V(2).Infof("planning: no update needed for repo=%s", repo)
		return &agentlib.Result{
			Status:    agentlib.AgentStatusDone,
			NextPhase: domain.TaskPhaseDone.String(),
		}, nil
	}

	if len(plan.GateTargets) == 0 {
		return needsInput("no gate target found in " + repo + " Makefile — " +
			"add a precommit/check/vulncheck target or handle manually"), nil
	}

	glog.V(2).Infof(
		"planning: ready repo=%s gate_targets=%v vulns=%d",
		repo, plan.GateTargets, len(plan.Vulns),
	)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseExecution.String(),
	}, nil
}

// runInspection issues the Claude planning call and parses the PlanOutput.
// Returns (plan, nil) on success or (nil, failResult) on runner/parse error.
func (s *planningStep) runInspection(
	ctx context.Context,
	md *agentlib.Markdown,
	workdir string,
) (*PlanOutput, *agentlib.Result) {
	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, failed("marshal task content: " + err.Error())
	}
	prompt := prompts.PlanningPrompt() +
		"\n\n## Workdir\n\n" + workdir +
		"\n\n## Target Go\n\n" + targetGoVersion() +
		"\n\n## Task\n\n" + taskContent
	runResult, err := s.runner.Run(ctx, prompt)
	if err != nil {
		glog.V(2).Infof("planning: claude runner failed: %v", err)
		return nil, failed("claude planning run: " + err.Error())
	}
	plan, err := parseJSONResponse[PlanOutput](ctx, runResult.Result)
	if err != nil {
		glog.V(2).Infof("planning: parse plan failed: %v", err)
		return nil, failed("parse planning output: " + err.Error())
	}
	return plan, nil
}

// failClone maps a clone error onto an actionable failed Result.
func (s *planningStep) failClone(repo string, err error) *agentlib.Result {
	if git.ClassifyError(err) == git.ErrorCategoryAuth {
		return failed("git auth failure — check App installation for " + repo)
	}
	return failed("clone failed: " + git.RedactToken(err.Error()))
}

// parkFindings returns the park-action vulns of the plan.
func parkFindings(plan *PlanOutput) []PlanVuln {
	var parked []PlanVuln
	for _, v := range plan.Vulns {
		if v.Action == VulnActionPark {
			parked = append(parked, v)
		}
	}
	return parked
}

// parkMessage assembles the design-D4 park escalation: every unfixable
// finding ID with its scanner, plus the three suppression surfaces an
// operator-approved suppression would touch.
func parkMessage(parked []PlanVuln) string {
	findings := make([]string, 0, len(parked))
	for _, v := range parked {
		entry := v.ID
		if v.Scanner != "" {
			entry += " (" + v.Scanner + ")"
		}
		if v.Reason != "" {
			entry += ": " + v.Reason
		}
		findings = append(findings, entry)
	}
	return fmt.Sprintf(
		"unfixable findings — suppress with justification or hold: %s; %s",
		strings.Join(findings, "; "),
		suppressionSurfacesHint,
	)
}

// writePlanSection marshals the typed ## Plan section into the task body.
func writePlanSection(ctx context.Context, md *agentlib.Markdown, plan *PlanOutput) error {
	section, err := agentlib.MarshalSectionTyped(ctx, "## Plan", *plan)
	if err != nil {
		return errors.Wrap(ctx, err, "marshal ## Plan section")
	}
	md.ReplaceSection(section)
	return nil
}

// readRequired pulls the required frontmatter fields. Returns the first
// missing field's name (or "" if all present), plus the resolved values.
// Empty string counts as missing.
func readRequired(md *agentlib.Markdown) (missing, repo, cloneURL, ref string) {
	values := map[string]string{}
	for _, key := range requiredFrontmatterFields {
		v, _ := md.Frontmatter.String(key)
		if strings.TrimSpace(v) == "" {
			return key, values["repo"], values["clone_url"], values["ref"]
		}
		values[key] = v
	}
	return "", values["repo"], values["clone_url"], values["ref"]
}

// setupWorkdir returns the canonical workdir path for the task and removes
// any stale copy from a prior run. Does NOT create the directory — the
// subsequent CloneAtRef call creates it. Deterministic per task so replays
// reuse the same slot.
func setupWorkdir(md *agentlib.Markdown, repo string) string {
	id, _ := md.Frontmatter.String("task_identifier")
	if strings.TrimSpace(id) == "" {
		id = strings.ReplaceAll(repo, "/", "-")
	}
	workdir := filepath.Join(os.TempDir(), workdirPrefix+sanitizePathComponent(id))
	if err := os.RemoveAll(workdir); err != nil {
		glog.Warningf("remove stale workdir failed: path=%s err=%v", workdir, err)
	}
	return workdir
}

// pathComponentRegexp keeps workdir names shell- and filesystem-safe.
var pathComponentRegexp = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func sanitizePathComponent(s string) string {
	return pathComponentRegexp.ReplaceAllString(s, "-")
}

// targetGoVersion returns the toolchain baked into this image (design D5:
// the image toolchain IS the bump target), without the "go" prefix.
func targetGoVersion() string {
	return strings.TrimPrefix(runtime.Version(), "go")
}

// parseJSONResponse and its supporting jsonFenceRegexp/lastJSONBlock live in
// llmjson.go — shared by planning (PlanOutput) and execution
// (executionReport) so a single fix covers both LLM sub-call parse sites.
