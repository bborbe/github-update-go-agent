// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory

// Test-only exports for the external factory_test package.
var (
	// ComputeChainTitle exposes the chained task's title builder so
	// factory_test can assert the emitted title survives
	// task.CreateCommand.Validate — the guard the slash form failed.
	ComputeChainTitle = computeChainTitle
	// BuildChainCommand exposes the full chain command assembly so the test
	// asserts the real published shape, not just the title helper in
	// isolation.
	BuildChainCommand = buildChainCommand
	// UpdaterPhases exposes the updater's phase assembly so the composition
	// test can assert the human-review owner wrapper is applied to the phase
	// bodies this agent actually runs. A direct wrapper test passes while the
	// wrapper is never composed — the defect the sibling agent shipped in #61.
	UpdaterPhases = updaterPhases
	// BuildFixPhases is the build-fixer's counterpart to UpdaterPhases.
	BuildFixPhases = buildFixPhases
)
