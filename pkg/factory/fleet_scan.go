// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

import (
	agentlib "github.com/bborbe/agent"
	domain "github.com/bborbe/vault-cli/pkg/domain"

	updatepkg "github.com/bborbe/github-update-go-agent/pkg"
)

// CreateFleetScanAgent composes the single-phase, LLM-free fleet scan agent.
func CreateFleetScanAgent(deps updatepkg.FleetScanDeps) *agentlib.Agent {
	return agentlib.NewAgent(fleetScanPhases(deps)...)
}

// fleetScanPhases is named (not an inline literal) so the composition test can
// assert the phase carries exactly one step and no Claude runner.
func fleetScanPhases(deps updatepkg.FleetScanDeps) []agentlib.Phase {
	return []agentlib.Phase{
		agentlib.NewPhase(domain.TaskPhaseExecution, updatepkg.NewFleetScanStep(deps)),
	}
}
