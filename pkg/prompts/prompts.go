// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package prompts provides embedded prompt modules for the Claude-backed
// phases: planning (inspect + classify) and execution (update + repair).
// The ai_review phase is pure Go and has no prompt.
package prompts

import (
	_ "embed"
)

//go:embed planning.md
var planning string

//go:embed execution.md
var execution string

// PlanningPrompt returns the planning-phase prompt module. The planning
// step appends the workdir, target Go version, and task content as context
// sections.
func PlanningPrompt() string {
	return planning
}

// ExecutionPrompt returns the execution-phase prompt module. The execution
// step appends the workdir, target Go version, and ## Plan JSON as context
// sections.
func ExecutionPrompt() string {
	return execution
}
