// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	stderrors "errors"
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
	"github.com/bborbe/github-update-go-agent/pkg/maintainerconfig"
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

// planningStep implements agentlib.Step for the planning phase: resolve the
// current default-branch HEAD at run start, clone at it, detect + run the
// repo's gate targets, parse the scanner findings into the ground-truth
// table, run the Claude inspection call against that table, validate every
// plan ID verbatim, classify, park-or-advance.
type planningStep struct {
	runner           claudelib.ClaudeRunner
	ops              git.GitOps
	gate             GateRunner
	ghToken          string `display:"length"`
	scope            InstallationScope
	maintainerConfig maintainerconfig.Fetcher
	defaultScope     UpdateScope
}

// NewPlanningStep wires the planning step with its Claude runner (inspection
// LLM), the GitOps seam (resolve the current default-branch HEAD at run
// start and clone at it), the GateRunner (repo gate detection + full
// scanner-output capture), the GitHub token (HTTPS auth URL transformation),
// the installation-scope allowlist check, the .maintainer.yaml consent
// gate (goUpdate.autoUpdate — mirror of github-releaser-agent spec 059),
// and the default update scope (UPDATE_SCOPE env; frontmatter `update_scope`
// overrides per task).
func NewPlanningStep(
	runner claudelib.ClaudeRunner,
	ops git.GitOps,
	gate GateRunner,
	ghToken string,
	scope InstallationScope,
	maintainerConfig maintainerconfig.Fetcher,
	defaultScope UpdateScope,
) agentlib.Step {
	return &planningStep{
		runner:           runner,
		ops:              ops,
		gate:             gate,
		ghToken:          ghToken,
		scope:            scope,
		maintainerConfig: maintainerConfig,
		defaultScope:     defaultScope,
	}
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
//  2. Resolve the repo's current default-branch HEAD at run start; clone at
//     it via GitOps → Failed naming the resolution step on resolution error,
//     or on clone/auth error.
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

	// Consent gate (F-?): a repo that has not opted into goUpdate.autoUpdate
	// in its .maintainer.yaml is skipped with a named reason — no update work,
	// no operator escalation. Runs before clone: the fetch is a contents-API
	// call, not a clone, so a skipped repo costs one HTTP round trip.
	if failResult := s.gateOnAutoUpdate(ctx, md, repo, ref); failResult != nil {
		return failResult, nil
	}

	// Resolve the update scope (frontmatter `update_scope` overrides the
	// UPDATE_SCOPE env default); an invalid value fails with the accepted set.
	updateScope, failResult := s.resolveScope(ctx, md)
	if failResult != nil {
		return failResult, nil
	}

	workdir := s.setupAndCleanupWorkdir(md, repo)

	if failResult := s.resolveAndClone(ctx, md, repo, cloneURL, ref, workdir); failResult != nil {
		return failResult, nil
	}

	if failResult := s.ciPinPreflight(ctx, workdir, repo); failResult != nil {
		return failResult, nil
	}

	plan, table, failResult := s.runInspection(ctx, md, workdir, updateScope)
	if failResult != nil {
		return failResult, nil
	}
	// Filter out-of-scope work out of has_work so a golang-only task with
	// stale deps (or a deps-only task with a stale directive) is not
	// classified ready.
	plan.appliesScope(updateScope)

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

	if s.shouldClose(plan, updateScope, repo) {
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

// setupAndCleanupWorkdir creates the ephemeral clone dir and registers its
// deferred cleanup on Run's stack. setupWorkdir removes any stale dir first.
func (s *planningStep) setupAndCleanupWorkdir(md *agentlib.Markdown, repo string) string {
	workdir := setupWorkdir(md, repo)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf("planning: workdir cleanup failed: path=%s err=%v", workdir, err)
		}
	}()
	return workdir
}

// resolveAndClone runs the run-start resolution (`git ls-remote --symref HEAD`)
// and clones the resolved HEAD into workdir. The pinned ref is recorded in
// the V(2) log alongside the resolved HEAD so the provenance stays observable
// but never reaches the clone base. A resolution failure stops planning here —
// the run does NOT fall back to the stale pinned ref (spec 004 failure mode 1).
func (s *planningStep) resolveAndClone(
	ctx context.Context,
	_ *agentlib.Markdown,
	repo, cloneURL, ref, workdir string,
) *agentlib.Result {
	authedURL := injectToken(normalizeCloneURLToHTTPS(cloneURL), s.ghToken)
	head, err := s.ops.ResolveDefaultBranchHead(ctx, authedURL)
	if err != nil {
		return s.failResolve(repo, err)
	}
	glog.V(2).Infof(
		"planning: pinned ref=%s (provenance only); clone base=resolved HEAD=%s",
		ref,
		head,
	)
	if err := s.ops.CloneAtRef(ctx, authedURL, head, workdir); err != nil {
		return s.failClone(repo, err)
	}
	return nil
}

// ciPinPreflight is the prevention gate for hardcoded CI Go toolchain pins.
// A `go-version:` value in a workflow freezes the CI toolchain; after a go.mod
// bump it fails CI (`go.mod requires go >= X (running go Y; GOTOOLCHAIN=local)`),
// and the agent is architecturally forbidden from editing workflows (no
// Workflows permission; committed-files guard rejects .github/workflows/**).
// So the agent escalates BEFORE any update work instead of opening a doomed
// PR. Matrix pins (deliberate multi-version testing) are not a hardcode.
// Returns a non-nil Result (needs_input escalation or failed) to short-circuit,
// or nil to proceed to inspection.
func (s *planningStep) ciPinPreflight(
	ctx context.Context,
	workdir, repo string,
) *agentlib.Result {
	pins, err := ScanWorkflowGoVersionPins(ctx, workdir)
	if err != nil {
		return failed("workflow pin scan failed: " + err.Error())
	}
	if !pins.HasPlainPin() {
		return nil
	}
	pin := pins.PlainPins[0]
	glog.V(2).
		Infof("planning: hardcoded go-version pin in %s=%s — escalating repo=%s", pin.File, pin.Value, repo)
	return needsInput("repo " + repo + " hardcodes CI Go toolchain in " + pin.File +
		" (go-version: " + pin.Value + "). The agent cannot edit workflows (no Workflows permission; " +
		"committed-files guard rejects .github/workflows/**). Fix manually: replace the single " +
		"`go-version:` value with `go-version-file: go.mod` (keep `cache: true`), push to master, " +
		"then re-delegate this task.")
}

// gateOnAutoUpdate is the .maintainer.yaml consent gate (mirror of
// github-releaser-agent's resolveMaintainerConfig, spec 059, adapted to the
// update-go skip semantics). A repo that has not opted into
// `goUpdate.autoUpdate` is skipped with a named reason — no update work, no
// operator escalation. Semantics:
//
//   - File absent (ErrFileNotFound)   → skip, reason `auto_update_disabled`, no warning
//   - Transport / 5xx / timeout       → skip + ConfigFetchWarning (non-fatal;
//     distinguishable from a deliberate false)
//   - Malformed YAML / non-boolean    → fail-closed: PlanOutcomeFailed +
//     ErrorCategoryInvalidConfig + InvalidField=goUpdate.autoUpdate, routed
//     to human_review (a config typo on a high-trust field must not be
//     silently downgraded to a skip)
//   - File present, AutoUpdate==false → skip, reason `auto_update_disabled`
//   - File present, AutoUpdate==true  → proceed (nil)
//
// Runs before clone: the fetch is a contents-API call, not a clone, so a
// skipped repo costs one HTTP round trip. The gate never writes
// .maintainer.yaml (read-only consent check).
func (s *planningStep) gateOnAutoUpdate(
	ctx context.Context,
	md *agentlib.Markdown,
	repo, ref string,
) *agentlib.Result {
	owner, name, ok := parseOwnerRepo(repo)
	if !ok {
		glog.V(2).Infof("planning: malformed repo=%q — escalating", repo)
		return failed("malformed repo " + repo + " (want owner/name)")
	}
	bytes, err := s.maintainerConfig.Fetch(ctx, owner, name, ref)
	if err != nil {
		if stderrors.Is(err, maintainerconfig.ErrFileNotFound) {
			glog.V(2).Infof(
				"planning: .maintainer.yaml absent at ref=%s — skipping repo=%s (auto_update_disabled)",
				ref, repo,
			)
			return s.skipAutoUpdate(
				ctx,
				md,
				"goUpdate.autoUpdate is absent (no .maintainer.yaml)",
				"",
			)
		}
		// Transport / non-404 error: skip + warning, NOT fail-closed (see
		// sibling spec 059 § Failure Modes — transient GitHub flakes are
		// usually recoverable; the operator can re-fire).
		glog.Warningf("planning: .maintainer.yaml fetch failed (treated as skip): %v", err)
		return s.skipAutoUpdate(
			ctx,
			md,
			".maintainer.yaml fetch failed (treated as auto_update_disabled)",
			".maintainer.yaml fetch failed (treated as goUpdate.autoUpdate=false): "+err.Error(),
		)
	}
	cfg, err := maintainerconfig.Parse(ctx, bytes)
	if err != nil {
		// YAML parse error or non-boolean value: fail-closed. Route to
		// human_review so an operator fixes the typo rather than the repo
		// silently dropping off the sweep.
		glog.V(2).Infof("planning: invalid .maintainer.yaml: field=goUpdate.autoUpdate err=%v", err)
		return s.failInvalidConfig(ctx, md, "goUpdate.autoUpdate", err)
	}
	if !cfg.GoUpdate.AutoUpdate {
		glog.V(2).Infof(
			"planning: goUpdate.autoUpdate=false — skipping repo=%s (auto_update_disabled)",
			repo,
		)
		return s.skipAutoUpdate(
			ctx,
			md,
			"goUpdate.autoUpdate is false in .maintainer.yaml",
			"",
		)
	}
	glog.V(2).Infof("planning: .maintainer.yaml consent OK repo=%s goUpdate.autoUpdate=true", repo)
	return nil
}

// skipAutoUpdate writes a no_update_needed ## Plan carrying the named
// auto_update_disabled reason and completes the task (Done/NextPhase done).
// A skip is a deliberate terminal, NOT an escalation: the repo opted out,
// so there is nothing for an operator to do.
func (s *planningStep) skipAutoUpdate(
	ctx context.Context,
	md *agentlib.Markdown,
	reason, warning string,
) *agentlib.Result {
	plan := &PlanOutput{
		Outcome:            PlanOutcomeNoUpdateNeeded,
		HasWork:            false,
		Reason:             "auto_update_disabled: " + reason,
		ConfigFetchWarning: warning,
	}
	if err := writePlanSection(ctx, md, plan); err != nil {
		glog.Warningf("planning: write skip plan failed: %v", err)
	}
	return &agentlib.Result{
		Status:    agentlib.AgentStatusDone,
		NextPhase: domain.TaskPhaseDone.String(),
		Message:   "auto_update_disabled: " + reason,
	}
}

// failInvalidConfig routes a malformed .maintainer.yaml to human_review with
// the typed invalid-config shape on the ## Plan block (mirror of the
// sibling's failInvalidConfig). The task page is the audit surface — a
// reader can grep for `error_category=invalid_config` on `## Plan`.
func (s *planningStep) failInvalidConfig(
	ctx context.Context,
	md *agentlib.Markdown,
	field string,
	cause error,
) *agentlib.Result {
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	plan := &PlanOutput{
		Outcome:       PlanOutcomeFailed,
		ErrorCategory: ErrorCategoryInvalidConfig,
		InvalidField:  field,
		InvalidValue:  extractInvalidValue(msg),
		Reason:        "invalid .maintainer.yaml: " + field,
	}
	if err := writePlanSection(ctx, md, plan); err != nil {
		glog.Warningf("planning: write invalid-config plan failed: %v", err)
	}
	return &agentlib.Result{
		Status:    agentlib.AgentStatusFailed,
		NextPhase: domain.TaskPhaseHumanReview.String(),
		Message:   "invalid .maintainer.yaml: " + field + ": " + msg,
	}
}

// extractInvalidValue pulls the raw bad value out of the wrapped parse error
// message so it lands verbatim in the task-page block. The yaml.v3 error
// format is e.g. "yaml: unmarshal errors: line 2: cannot unmarshal !!str
// `yes` into bool". We surface the offending token; on parse-format drift,
// fall back to the full error string so the field is never blank.
func extractInvalidValue(msg string) string {
	if i := strings.Index(msg, "`"); i >= 0 {
		if j := strings.Index(msg[i+1:], "`"); j >= 0 {
			return msg[i+1 : i+1+j]
		}
	}
	return msg
}

// parseOwnerRepo splits "owner/name" into its two segments. Returns ok=false
// on malformed input (missing slash, empty segment).
func parseOwnerRepo(s string) (owner, name string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
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
	updateScope UpdateScope,
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

	// Drop operator-approved no-fix suppressions from the table before the
	// model sees it. The gate targets run against the repo's suppression
	// configs and pass, yet their echoed output can still list the suppressed
	// IDs — captured naively, planning re-parks on a suppression the operator
	// already approved (design D4; observed 2026-08-24: hue's planning parked
	// on all 8 IDs present in .osv-scanner.toml despite the gate passing).
	suppressed, err := loadSuppressedVulnIDs(ctx, workdir)
	if err != nil {
		return nil, nil, failed("load suppressed vuln IDs: " + err.Error())
	}
	if len(suppressed) > 0 {
		table = table.FilterSuppressed(suppressed)
		glog.V(2).
			Infof("planning: filtered %d suppressed vuln IDs from scanner table", len(suppressed))
	}

	taskContent, err := md.Marshal(ctx)
	if err != nil {
		return nil, nil, failed("marshal task content: " + err.Error())
	}
	prompt := prompts.PlanningPrompt() +
		"\n\n## Workdir\n\n" + workdir +
		"\n\n## Target Go\n\n" + targetGoVersion() +
		"\n\n" + updateScopeSection(updateScope) +
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
	// Drop operator-approved no-fix suppressions from the plan's vulns the
	// same way they were dropped from the table. The model may still echo a
	// suppressed ID (it appears in the task body's prior ## Failure text even
	// though it is absent from the scanner table); validating such a plan
	// against the filtered table would hard-fail instead of parking, replacing
	// the fixed re-park with a new failure (observed 2026-08-24: hue #1f653078
	// "plan validation: vuln id ... not found in captured scanner output").
	if len(suppressed) > 0 {
		plan.Vulns = filterSuppressedVulns(plan.Vulns, suppressed)
	}
	if err := validatePlanAgainstTable(ctx, plan, table); err != nil {
		return nil, nil, failed("plan validation: " + err.Error())
	}
	return plan, table, nil
}

// filterSuppressedVulns returns a copy of vulns without entries whose ID is in
// suppressed. Operator-approved no-fix suppressions are not actionable and must
// not drive parking; the model echoing one from prior failure text should be
// ignored, not fail validation.
func filterSuppressedVulns(vulns []PlanVuln, suppressed map[string]bool) []PlanVuln {
	if len(suppressed) == 0 {
		return vulns
	}
	filtered := make([]PlanVuln, 0, len(vulns))
	for _, v := range vulns {
		if suppressed[v.ID] {
			continue
		}
		filtered = append(filtered, v)
	}
	return filtered
}

// failClone maps a clone error onto an actionable failed Result.
func (s *planningStep) failClone(repo string, err error) *agentlib.Result {
	if git.ClassifyError(err) == git.ErrorCategoryAuth {
		return failed("git auth failure — check App installation for " + repo)
	}
	return failed("clone failed: " + git.RedactToken(err.Error()))
}

// failResolve maps a current-HEAD resolution error onto a failed Result that
// names the resolution step. The run must NOT fall back to the stale pinned
// ref (spec 004 failure mode 1) — a resolution failure stops planning here.
func (s *planningStep) failResolve(repo string, err error) *agentlib.Result {
	return failed(
		"resolve current default-branch HEAD for " + repo + ": " + git.RedactToken(err.Error()),
	)
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

// resolveUpdateScope reads the task's optional `update_scope` frontmatter
// field and resolves it against the deployment default. An absent or empty
// field resolves to defaultScope; a present-but-invalid value returns an
// error naming the accepted set (the caller surfaces it as a failed result —
// planning and execution must agree on scope before any update work runs).
func resolveUpdateScope(
	ctx context.Context,
	md *agentlib.Markdown,
	defaultScope UpdateScope,
) (UpdateScope, error) {
	v, _ := md.Frontmatter.String("update_scope")
	if strings.TrimSpace(v) == "" {
		return defaultScope, nil
	}
	return ParseUpdateScope(ctx, v)
}

// resolveScope is the planning step's scope-resolution wrapper: it resolves
// the update scope and returns a failed Result when the frontmatter value is
// invalid (the accepted-set error is the message), keeping Run lean.
// shouldClose reports whether planning ends the task instead of routing to
// execution. The decision is driven by the plan's structured fields, never by
// the model's `outcome` label alone.
//
// A label sitting in front of the scope check as an `||` can only ever widen
// the close: on 2026-08-19 bborbe/argument (update_scope=deps) came back
// `no_update_needed` + `has_work=false` while the SAME object carried
// `dep_updates_expected=true`, and its reason had the scope inverted ("dep
// updates are out of scope since update_scope is deps"). The task completed
// with no PR and never retried, while the repo really had three direct dep
// updates waiting. Trusting a checkable field over a prose verdict is the same
// rule the close-obsolete-tasks evidence gate follows.
//
// Cost of being wrong in this direction is bounded: if the fields overstate the
// work, execution's no-effective-change guard writes no_update_needed and routes
// to done. A wasted pass beats a silent skip.
func (s *planningStep) shouldClose(plan *PlanOutput, scope UpdateScope, repo string) bool {
	hasWork := plan.hasWorkForScope(scope)
	if plan.Outcome == PlanOutcomeNoUpdateNeeded && hasWork {
		glog.V(0).Infof(
			"planning: model claimed no_update_needed but plan fields show in-scope work — "+
				"overriding to ready repo=%s scope=%s dep_updates_expected=%t vulns=%d go_bump=%t reason=%q",
			repo, scope, plan.DepUpdatesExpected, len(plan.Vulns), plan.GoBump != nil, plan.Reason,
		)
	}
	if hasWork {
		return false
	}
	glog.V(2).Infof("planning: no update needed for repo=%s scope=%s", repo, scope)
	return true
}

func (s *planningStep) resolveScope(
	ctx context.Context,
	md *agentlib.Markdown,
) (UpdateScope, *agentlib.Result) {
	scope, err := resolveUpdateScope(ctx, md, s.defaultScope)
	if err != nil {
		glog.V(2).Infof("planning: invalid update_scope — failing: %v", err)
		return UpdateScope(""), failed("invalid update_scope: " + err.Error())
	}
	glog.V(2).Infof("planning: update_scope=%s", scope)
	return scope, nil
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
