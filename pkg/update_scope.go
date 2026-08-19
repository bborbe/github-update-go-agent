// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	"github.com/bborbe/errors"
)

// UpdateScope is the closed set of update-work scopes the agent may apply to
// a repo. It selects what the update sequence touches: the Go toolchain
// directive, the module dependency graph, or both. Chosen per task from
// frontmatter `update_scope` (falling back to the UPDATE_SCOPE env default).
type UpdateScope string

const (
	// UpdateScopeBoth updates the Go toolchain directive AND the dependency
	// graph (the default — matches the pre-knob behaviour byte-for-byte).
	UpdateScopeBoth UpdateScope = "both"
	// UpdateScopeGolang updates only the Go toolchain directive (go.mod
	// `go`/`toolchain` + Dockerfile golang pins); deps are left untouched.
	UpdateScopeGolang UpdateScope = "golang"
	// UpdateScopeDeps updates only the module dependency graph; the Go
	// toolchain directive is left untouched.
	UpdateScopeDeps UpdateScope = "deps"
)

// UpdateScopes is a collection of UpdateScope values.
type UpdateScopes []UpdateScope

// AvailableUpdateScopes lists every UpdateScope the agent accepts. Validate
// ranges over this collection — it is the single source of truth.
var AvailableUpdateScopes = UpdateScopes{UpdateScopeBoth, UpdateScopeGolang, UpdateScopeDeps}

// Contains returns true if the collection contains scope.
func (u UpdateScopes) Contains(scope UpdateScope) bool {
	for _, v := range u {
		if v == scope {
			return true
		}
	}
	return false
}

// Strings returns a slice of string representations of each UpdateScope.
func (u UpdateScopes) Strings() []string {
	result := make([]string, len(u))
	for i, v := range u {
		result[i] = v.String()
	}
	return result
}

// String returns the string representation of the UpdateScope.
func (u UpdateScope) String() string {
	return string(u)
}

// IsBoth returns true if the UpdateScope is UpdateScopeBoth.
func (u UpdateScope) IsBoth() bool {
	return u == UpdateScopeBoth
}

// IsGolangOnly returns true if the UpdateScope is UpdateScopeGolang.
func (u UpdateScope) IsGolangOnly() bool {
	return u == UpdateScopeGolang
}

// IsDepsOnly returns true if the UpdateScope is UpdateScopeDeps.
func (u UpdateScope) IsDepsOnly() bool {
	return u == UpdateScopeDeps
}

// Validate returns nil when the UpdateScope is a member of
// AvailableUpdateScopes, otherwise returns an error naming the rejected value
// and the accepted set.
func (u UpdateScope) Validate(ctx context.Context) error {
	if !AvailableUpdateScopes.Contains(u) {
		return errors.Errorf(
			ctx,
			"unknown update scope %q accepted values are: %v",
			u,
			AvailableUpdateScopes.Strings(),
		)
	}
	return nil
}

// ParseUpdateScope resolves the configured update scope. An empty value
// means "unset" and resolves to UpdateScopeBoth, preserving the
// pre-knob behaviour byte-for-byte. Any other unrecognised value is rejected.
func ParseUpdateScope(ctx context.Context, value string) (UpdateScope, error) {
	if value == "" {
		return UpdateScopeBoth, nil
	}
	scope := UpdateScope(value)
	if err := scope.Validate(ctx); err != nil {
		return UpdateScope(""), err
	}
	return scope, nil
}

// updateScopeSection renders the `## Update Scope` context section appended
// to the planning and execution prompts. It tells the model which update
// steps are in scope and which are out, so a golang-only or deps-only sweep
// does not drift into the excluded work. The "both" default matches the
// pre-knob prompt content byte-for-byte.
func updateScopeSection(scope UpdateScope) string {
	switch scope {
	case UpdateScopeGolang:
		return "## Update Scope\n\n" +
			"Update ONLY the Go toolchain: the go.mod `go` directive (and Dockerfile " +
			"golang pins). Do NOT update module dependencies: skip `go get -u ./...`, " +
			"do not run dependency upgrades, and do not touch go.sum for non-toolchain " +
			"reasons. `dep_updates_expected` is out of scope — it must NOT contribute to " +
			"has_work, and vuln fixes requiring a module bump are out of scope too."
	case UpdateScopeDeps:
		return "## Update Scope\n\n" +
			"Update ONLY module dependencies: `go get -u ./...`, targeted vuln fixes " +
			"(`go get <pkg>@<fixed>`), and `go mod tidy`. Do NOT bump the go.mod `go` " +
			"directive and do NOT touch Dockerfile golang pins. The go-directive bump is " +
			"out of scope — it must NOT contribute to has_work. Dependency updates " +
			"ARE in scope: if `dep_updates_expected` is true, `has_work` MUST be true " +
			"and `outcome` MUST NOT be `no_update_needed`. Do not write that dep " +
			"updates are out of scope on a deps-scope run — that is inverted."
	default:
		return "## Update Scope\n\n" +
			"Update BOTH the Go toolchain directive and module dependencies."
	}
}

// appliesScope filters an out-of-scope plan item out of has_work. Called on
// the parsed plan so a golang-only task with stale deps is not classified as
// ready, and a deps-only task with a stale directive is not classified as
// ready either.
func (p *PlanOutput) appliesScope(scope UpdateScope) {
	switch scope {
	case UpdateScopeGolang:
		p.DepUpdatesExpected = false
		p.Vulns = onlyGolangScopedVulns(p.Vulns)
	case UpdateScopeDeps:
		p.GoBump = nil
	}
}

// onlyGolangScopedVulns keeps only the vulns a golang-only scope can act on.
// A vuln whose fix requires a module bump is out of scope for golang-only —
// it stays visible in the table but does not make the task "ready".
func onlyGolangScopedVulns(vulns []PlanVuln) []PlanVuln {
	filtered := make([]PlanVuln, 0, len(vulns))
	for _, v := range vulns {
		if v.Package == "" {
			// stdlib vulns are fixed by the toolchain bump — in scope.
			filtered = append(filtered, v)
		}
	}
	return filtered
}

// hasWork returns whether any in-scope work remains for the given scope.
// A golang-only task ignores dep work; a deps-only task ignores the
// directive bump.
func (p *PlanOutput) hasWorkForScope(scope UpdateScope) bool {
	switch scope {
	case UpdateScopeGolang:
		return p.GoBump != nil || len(onlyGolangScopedVulns(p.Vulns)) > 0
	case UpdateScopeDeps:
		return p.DepUpdatesExpected || len(p.Vulns) > 0
	default:
		return p.GoBump != nil || p.DepUpdatesExpected || len(p.Vulns) > 0
	}
}
