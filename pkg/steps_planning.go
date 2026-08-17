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
// ref, detect + run the repo's gate targets, parse the scanner findings into
// the ground-truth table, run the Claude inspection call against that table,
// validate every plan ID verbatim, classify, park-or-advance.
type planningStep struct {
	runner  claudelib.ClaudeRunner
	ops     git.GitOps
	gate    GateRunner
	ghToken string
	scope   InstallationScope
}

// NewPlanningStep wires the planning step with its Claude runner (inspection
// LLM), the GitOps seam (clone at ref), the GateRunner (repo gate detection
// + full scanner-output capture), the GitHub token (HTTPS auth URL
// transformation), and the installation-scope allowlist check.
func NewPlanningStep(
	runner claudelib.ClaudeRunner,
	ops git.GitOps,
	gate GateRunner,
	ghToken string,
	scope InstallationScope,
) agentlib.Step {
	return &planningStep{runner: runner, ops: ops, gate: gate, ghToken: ghToken, scope: scope}
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
//  3. Detect gate targets from the Makefile in Go, run each via the
//     GateRunner, and capture the full raw scanner output (no gate target →
//     NeedsInput; a failing target with no parseable findings → Failed).
//  4. Claude inspection call against the parsed Scanner Findings table →
//     parse PlanOutput; validate every vuln ID verbatim against the table
//     (a fabricated or prefix-colliding ID → Failed naming it).
//  5. Any park-action finding → ## Plan + NeedsInput naming the verbatim
//     scanner rows and the three suppression surfaces (design D4).
//  6. no_update_needed → ## Plan + Done/NextPhase done (task completes).
//  7. ready → ## Plan + Done/NextPhase execution.
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

	plan, table, failResult := s.runInspection(ctx, md, workdir)
	if failResult != nil {
		return failResult, nil
	}

	if parked := parkFindings(plan); len(parked) > 0 {
		if err := writePlanSection(ctx, md, plan); err != nil {
			return nil, err
		}
		msg := parkMessage(parked, table)
		glog.V(2).Infof("planning: parking task — %s", msg)
		return needsInput(msg), nil
	}

	if plan.Outcome == PlanOutcomeNeedsInput {
		if refuteEnvironmentClaim(workdir, plan.Reason) {
			glog.V(2).
				Infof("planning: env-claim refuted — workdir exists, not clearing assignee: reason=%q", plan.Reason)
			return failed(
				"needs_input reason claims an environment problem but workdir " + workdir + " exists on disk — false claim, not clearing assignee: " + plan.Reason,
			), nil
		}
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

	glog.V(2).Infof(
		"planning: ready repo=%s gate_targets=%v vulns=%d",
		repo, plan.GateTargets, len(plan.Vulns),
	)
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseExecution.String(),
	}, nil
}

// runInspection detects the repo's gate targets in Go, runs each one via
// the GateRunner, parses the raw output into the ground-truth findings
// table, embeds the table in the planning prompt as the only source of
// advisory IDs, runs the Claude sub-call, and validates every plan vuln ID
// verbatim against the table. Returns (plan, table, nil) on success or
// (nil, nil, failResult) on runner/parse/validation error.
func (s *planningStep) runInspection(
	ctx context.Context,
	md *agentlib.Markdown,
	workdir string,
) (*PlanOutput, ScannerTable, *agentlib.Result) {
	targets, err := detectGateTargets(ctx, workdir)
	if err != nil {
		return nil, nil, failed("detect gate targets: " + err.Error())
	}
	repo, _ := md.Frontmatter.String("repo")
	if len(targets) == 0 {
		return nil, nil, needsInput("no gate target found in " + repo + " Makefile — " +
			"add a precommit/check/vulncheck target or handle manually")
	}

	table := ScannerTable{}
	for _, target := range targets {
		select {
		case <-ctx.Done():
			return nil, nil, failed("canceled while running gate targets: " + ctx.Err().Error())
		default:
		}
		output, exitCode, runErr := s.gate.RunTargetFull(ctx, workdir, target)
		rows := parseScannerOutput(target, output)
		if runErr != nil && len(rows) == 0 {
			glog.V(2).
				Infof("planning: gate target %s failed exit=%d rows=0 output=%q", target, exitCode, output)
			return nil, nil, failed(fmt.Sprintf(
				"gate target %q failed (exit %d) with no parseable findings: %s",
				target, exitCode, truncateTail(output, gateTailMaxBytes),
			))
		}
		table = append(table, rows...)
	}

	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, nil, failed("marshal task content: " + err.Error())
	}
	prompt := prompts.PlanningPrompt() +
		"\n\n## Workdir\n\n" + workdir +
		"\n\n## Target Go\n\n" + targetGoVersion() +
		"\n\n## Scanner Findings\n\nThe findings below were captured by Go from running the repo's own gate targets — they are the ONLY source of advisory IDs. Every vuln ID you report MUST appear in this table verbatim.\n\n" + renderScannerTable(table) +
		"\n\n## Task\n\n" + taskContent
	runResult, err := s.runner.Run(ctx, prompt)
	if err != nil {
		glog.V(2).Infof("planning: claude runner failed: %v", err)
		return nil, nil, failed("claude planning run: " + err.Error())
	}
	plan, err := parseJSONResponse[PlanOutput](ctx, runResult.Result)
	if err != nil {
		glog.V(2).Infof("planning: parse plan failed: %v", err)
		return nil, nil, failed("parse planning output: " + err.Error())
	}
	plan.GateTargets = targets
	if err := validatePlanAgainstTable(ctx, plan, table); err != nil {
		return nil, nil, failed("plan validation: " + err.Error())
	}
	return plan, table, nil
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
// finding ID with its verbatim scanner row (id, scanner, fixed version)
// from the captured table, plus the three suppression surfaces an
// operator-approved suppression would touch. The model's prose reason is
// deliberately NOT carried — the operator's suppression decision must be
// made against the real finding.
func parkMessage(parked []PlanVuln, table ScannerTable) string {
	findings := make([]string, 0, len(parked))
	for _, v := range parked {
		row, ok := table.Row(v.ID)
		if !ok {
			row = ScannerFinding{ID: v.ID, Scanner: v.Scanner}
		}
		entry := row.ID + " (scanner=" + row.Scanner + ", fixed_version=" + row.FixedVersion + ")"
		findings = append(findings, entry)
	}
	return fmt.Sprintf(
		"unfixable findings — suppress with justification or hold: %s; %s",
		strings.Join(findings, "; "),
		suppressionSurfacesHint,
	)
}

// environmentClaimMarkers are needs_input reason substrings that claim an
// environment problem (workdir/sandbox/permission) rather than a real repo
// finding. Such a claim is stat-verified before it may clear the assignee.
var environmentClaimMarkers = []string{
	"workdir",
	"sandbox",
	"permission",
	"allowed paths",
	"filesystem access",
}

// claimsEnvironmentProblem reports whether reason reads as an environment
// claim (case-insensitive marker match).
func claimsEnvironmentProblem(reason string) bool {
	lowered := strings.ToLower(reason)
	for _, marker := range environmentClaimMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// refuteEnvironmentClaim stat-checks the agent's own workdir against an
// environment-claim needs_input reason. The planning step cloned into
// workdir, so an existing workdir refutes a "cannot access workdir /
// sandbox" claim. Logs the stat result alongside the claim at V(2) so the
// evidence is visible in the deployed pod log.
func refuteEnvironmentClaim(workdir string, reason string) bool {
	if !claimsEnvironmentProblem(reason) {
		return false
	}
	_, err := os.Stat(workdir)
	exists := err == nil
	glog.V(2).
		Infof("planning: env-claim check workdir=%s stat_exists=%t reason=%q", workdir, exists, reason)
	return exists
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
