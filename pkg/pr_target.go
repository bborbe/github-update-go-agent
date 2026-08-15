// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	"github.com/bborbe/errors"
)

// PRTarget is the closed set of pull-request targets the agent may open a
// pull request as. It is chosen per deployment at creation time only — the
// agent never flips an already-open pull request and never merges.
type PRTarget string

const (
	// PRTargetDraft opens the pull request as a draft (the default).
	PRTargetDraft PRTarget = "draft"
	// PRTargetReady opens the pull request ready for review.
	PRTargetReady PRTarget = "ready"
)

// PRTargets is a collection of PRTarget values.
type PRTargets []PRTarget

// AvailablePRTargets lists every PRTarget the agent accepts. Validate
// ranges over this collection — it is the single source of truth.
var AvailablePRTargets = PRTargets{PRTargetDraft, PRTargetReady}

// Contains returns true if the collection contains target.
func (p PRTargets) Contains(target PRTarget) bool {
	for _, v := range p {
		if v == target {
			return true
		}
	}
	return false
}

// Strings returns a slice of string representations of each PRTarget.
func (p PRTargets) Strings() []string {
	result := make([]string, len(p))
	for i, v := range p {
		result[i] = v.String()
	}
	return result
}

// String returns the string representation of the PRTarget.
func (p PRTarget) String() string {
	return string(p)
}

// IsDraft returns true if the PRTarget is PRTargetDraft.
func (p PRTarget) IsDraft() bool {
	return p == PRTargetDraft
}

// Validate returns nil when the PRTarget is a member of AvailablePRTargets,
// otherwise returns an error naming the rejected value and the accepted set.
func (p PRTarget) Validate(ctx context.Context) error {
	if !AvailablePRTargets.Contains(p) {
		return errors.Errorf(
			ctx,
			"unknown pull request target %q accepted values are: %v",
			p,
			AvailablePRTargets.Strings(),
		)
	}
	return nil
}

// ParsePRTarget resolves the configured pull-request target. An empty
// value means "unset" and resolves to PRTargetDraft, preserving the
// pre-configuration behaviour byte-for-byte. Any other unrecognised value
// is rejected.
func ParsePRTarget(ctx context.Context, value string) (PRTarget, error) {
	if value == "" {
		return PRTargetDraft, nil
	}
	target := PRTarget(value)
	if err := target.Validate(ctx); err != nil {
		return PRTarget(""), err
	}
	return target, nil
}
