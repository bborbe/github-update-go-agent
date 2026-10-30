// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	agentlib "github.com/bborbe/agent"
	domain "github.com/bborbe/vault-cli/pkg/domain"
)

// reassignDeclarationKey names the frontmatter key that declares an assignee
// write as a deliberate reassignment rather than an echo of the emitter's
// spawn-time snapshot. agent-task-controller's operator-owned-assignee guard
// keeps the on-disk assignee over a differing incoming one unless the payload
// carries this key, and it consumes the key once honoured so it never lands on
// the task. The spelling is a frozen cross-repo contract with that guard
// (agent-task-controller pkg/result/result_writer.go reassignmentDeclarationKey);
// renaming it here silently reverts every stamp to a discard, with no error
// anywhere.
const reassignDeclarationKey = "assignee_reassigned_by"

// humanReviewOwnerStep stamps a human owner onto every result this agent routes
// into human_review.
//
// Why a wrapper rather than three edits: this binary reaches that phase from
// three call sites across three steps — the ai_review approval (steps_review.go),
// the malformed-config failure in planning (steps_planning.go), and the build-fix
// review hand-off (steps_fix_review.go). Wrapping each composed phase body covers
// all three at one seam and leaves the routing logic untouched, so a new
// human_review return added inside a step is covered automatically.
//
// Why the owner is written at all: the controller's human_review chokepoint
// clears the assignee so an agent cannot keep a record it parked. Nothing wrote
// a human back in, so a record sat in neither the human's queue nor any agent's
// while its PR stayed open. The stamp and the controller's configured
// HUMAN_ASSIGNEES set are two halves of one mechanism — without the controller
// half the stamp is wiped on the next result write, and without the stamp the
// controller has nothing to retain.
//
// Why this does not contradict docs/design.md § 7.1: that doctrine ("the agent
// never writes ## Failure, never mutates assignee/status") governs the *failure*
// path, where the controller owns the envelope. This wrapper never clears an
// assignee, never touches status, and never routes to human_review on failure —
// it only names the person a success-path hand-off is waiting for.
type humanReviewOwnerStep struct {
	inner agentlib.Step
	owner string
}

// NewHumanReviewOwnerStep wraps inner so that a human_review result carries
// owner as the assignee. An empty owner disables the stamp (the wrapper then
// behaves exactly like inner), which is the unconfigured default: no person's
// name is hard-coded here, and the deploy supplies the value.
func NewHumanReviewOwnerStep(inner agentlib.Step, owner string) agentlib.Step {
	if owner == "" {
		return inner
	}
	return &humanReviewOwnerStep{inner: inner, owner: owner}
}

// IsHumanReviewOwnerStep reports whether step is a human-review owner wrapper
// and, when it is, the owner it stamps.
//
// It exists for composition tests. The phase list is composed in the factory, so
// a factory that forgets to wrap one of the phase bodies is invisible to any test
// that exercises the wrapper directly — which is exactly how the unread-owner
// defect reached review in the sibling agent. Production code never calls it.
func IsHumanReviewOwnerStep(step agentlib.Step) (string, bool) {
	s, ok := step.(*humanReviewOwnerStep)
	if !ok {
		return "", false
	}
	return s.owner, true
}

// Name implements agentlib.Step by delegating, so wrapping a step does not
// change the name the phase machinery reports for it.
func (s *humanReviewOwnerStep) Name() string { return s.inner.Name() }

// ShouldRun implements agentlib.Step by delegating — the wrapper adds no
// precondition of its own.
func (s *humanReviewOwnerStep) ShouldRun(
	ctx context.Context,
	md *agentlib.Markdown,
) (bool, error) {
	return s.inner.ShouldRun(ctx, md)
}

// Run implements agentlib.Step. The inner result and error are returned
// unchanged; the only side effect is the two frontmatter keys, written only for
// a result that routes into human_review.
func (s *humanReviewOwnerStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	result, err := s.inner.Run(ctx, md)
	// The domain constant, not a local literal: the three routing sites build
	// NextPhase from domain.TaskPhaseHumanReview.String(), and a hand-written
	// "human_review" here would drift from that enum silently.
	if result == nil || result.NextPhase != domain.TaskPhaseHumanReview.String() {
		return result, err
	}
	if md.Frontmatter == nil {
		md.Frontmatter = agentlib.TaskFrontmatter{}
	}
	md.Frontmatter["assignee"] = s.owner
	// Without this key the controller's operator-owned-assignee guard keeps the
	// on-disk agent assignee and discards the one above, so the stamp would be
	// a silent no-op.
	md.Frontmatter[reassignDeclarationKey] = s.Name()
	return result, err
}
