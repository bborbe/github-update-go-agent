// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"os/exec"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
)

// Test-only exports for the external pkg_test package.
var (
	NormalizeCloneURLToHTTPS = normalizeCloneURLToHTTPS
	InjectToken              = injectToken
	PRCreateArgs             = prCreateArgs
	IsMissingLabelError      = isMissingLabelError
	ParseScannerOutput       = parseScannerOutput
	DetectGateTargets        = detectGateTargets
	ValidatePlanAgainstTable = validatePlanAgainstTable
	RenderScannerTable       = renderScannerTable
	ParkMessage              = parkMessage
	ExtractToolResultBodies  = extractToolResultBodies
	PlanningResultText       = planningResultText
	ScanPlanningOutput       = scanPlanningOutput
	RefuteEnvironmentClaim   = refuteEnvironmentClaim
	BuildYourMoveBody        = buildYourMoveBody
	WriteYourMoveSection     = writeYourMoveSection
	// HasWorkForScope / AppliesScope expose the scope-aware plan predicates so
	// pkg_test can assert the close decision is driven by the plan's structured
	// fields rather than the model's `outcome` label.
	HasWorkForScope = func(p *PlanOutput, scope UpdateScope) bool { return p.hasWorkForScope(scope) }
	// ValidatePlan exposes execution's plan-readiness guard so pkg_test can
	// assert it mirrors planning's scope-aware decision rather than the model's
	// outcome/HasWork labels. Accepts the unexported step via the exported
	// constructor return type and type-asserts.
	ValidatePlan = func(step agentlib.Step, md *agentlib.Markdown, scope UpdateScope) (*PlanOutput, error) {
		es, ok := step.(*executionStep)
		if !ok {
			return nil, fmt.Errorf("expected *executionStep, got %T", step)
		}
		return es.validatePlan(context.Background(), md, scope)
	}
	AppliesScope = func(p *PlanOutput, scope UpdateScope) { p.appliesScope(scope) }
	// ShouldClose exposes planning's close decision. This is the predicate the
	// bborbe/argument regression actually turned on — hasWorkForScope alone
	// behaved identically before and after the fix, so a test that only exercises
	// it would pass against the buggy code.
	ShouldClose = func(plan *PlanOutput, scope UpdateScope, repo string) bool {
		return (&planningStep{}).shouldClose(plan, scope, repo)
	}
	// PlanningRunnerForTest constructs a planningRunner with an injected log
	// sink — the sink is constructor-injected, never swapped package state.
	PlanningRunnerForTest = func(config claudelib.ClaudeRunnerConfig, sink func(context.Context, string)) *planningRunner {
		return &planningRunner{config: config, logSink: sink}
	}
	PlanningRunnerBuildCmd = func(r *planningRunner, ctx context.Context, prompt string) (*exec.Cmd, error) {
		return r.buildCommand(ctx, prompt)
	}
)

// LLMJSONProbe is the typed shape pkg_test uses to exercise
// parseJSONResponse's three extraction strategies without depending on
// PlanOutput/executionReport internals.
type LLMJSONProbe struct {
	Foo string `json:"foo"`
	Bar int    `json:"bar"`
}

// ParseLLMJSONProbe wraps the unexported generic parseJSONResponse,
// instantiated for LLMJSONProbe, so pkg_test can exercise it directly.
func ParseLLMJSONProbe(ctx context.Context, response string) (*LLMJSONProbe, error) {
	return parseJSONResponse[LLMJSONProbe](ctx, response)
}
