// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import "strings"

// ErrorCategory is the closed enum returned by ClassifyError. The steps
// branch on these values to compose actionable failure messages.
//
// The set is CLOSED — adding a new category requires a design amendment.
type ErrorCategory string

const (
	// ErrorCategoryAuth — git server rejected credentials (401/403, missing username).
	ErrorCategoryAuth ErrorCategory = "auth"
	// ErrorCategoryRepoNotFound — clone target does not exist on the server (404).
	// NOTE: GitHub returns "Repository not found" for BOTH a typo'd repo URL
	// AND an unauthenticated request for a private repo. The watcher emit +
	// App-IAT auth eliminate the private-repo confounder upstream, so a 404
	// truly means the repo does not exist (mirrors github-releaser-agent).
	ErrorCategoryRepoNotFound ErrorCategory = "repo_not_found"
	// ErrorCategoryProtectedBranchRejected — branch protection rejected the push.
	ErrorCategoryProtectedBranchRejected ErrorCategory = "protected_branch_rejected"
	// ErrorCategoryPushNonFastForward — remote moved between clone and push;
	// controller retry will re-fetch.
	ErrorCategoryPushNonFastForward ErrorCategory = "push_non_fast_forward"
	// ErrorCategoryUnexpectedDiff — the update commit touched a forbidden path
	// (.github/workflows/**). Set DIRECTLY by the execution step's
	// committed-files guard, NOT by ClassifyError — it is a semantic assertion
	// on the file set, so there is no stderr fragment to match.
	ErrorCategoryUnexpectedDiff ErrorCategory = "unexpected_diff"
	// ErrorCategoryUnknown — message does not match any known fragment. Bug
	// signal: if this fires repeatedly, add a new substring to the table.
	ErrorCategoryUnknown ErrorCategory = "unknown"
)

// classifierEntry maps a substring fragment to a category. Order matters:
// more-specific fragments must come first (protected-branch tokens before
// generic push errors).
type classifierEntry struct {
	Fragment string
	Category ErrorCategory
}

// classifierTable is the canonical substring→category mapping. Distinct
// fragment per category — adding entries requires a design amendment.
var classifierTable = []classifierEntry{
	// Protected-branch fragments (push step).
	{Fragment: "protected branch", Category: ErrorCategoryProtectedBranchRejected},
	{Fragment: "GH006", Category: ErrorCategoryProtectedBranchRejected},
	{Fragment: "Required reviews", Category: ErrorCategoryProtectedBranchRejected},
	{Fragment: "required status checks", Category: ErrorCategoryProtectedBranchRejected},
	// Non-fast-forward (push step).
	{Fragment: "non-fast-forward", Category: ErrorCategoryPushNonFastForward},
	{
		Fragment: "Updates were rejected because the remote contains work",
		Category: ErrorCategoryPushNonFastForward,
	},
	// Repo not found (clone step).
	{Fragment: "Repository not found", Category: ErrorCategoryRepoNotFound},
	{Fragment: "returned error: 404", Category: ErrorCategoryRepoNotFound},
	// Auth (clone step).
	{Fragment: "Authentication failed", Category: ErrorCategoryAuth},
	{Fragment: "could not read Username", Category: ErrorCategoryAuth},
	{Fragment: "returned error: 403", Category: ErrorCategoryAuth},
	{Fragment: "returned error: 401", Category: ErrorCategoryAuth},
}

// ClassifyError maps a git stderr-wrapped error to the closed enum.
//
// Returns the empty-string sentinel `ErrorCategory("")` when err is nil —
// this distinguishes "no error to classify" from ErrorCategoryUnknown
// ("an error occurred but no fragment matched").
//
// unexpected_diff is NEVER returned by this function — that category is set
// by the execution step's guard layer, not at the git-stderr layer.
func ClassifyError(err error) ErrorCategory {
	if err == nil {
		return ErrorCategory("")
	}
	msg := err.Error()
	for _, entry := range classifierTable {
		if strings.Contains(msg, entry.Fragment) {
			return entry.Category
		}
	}
	return ErrorCategoryUnknown
}
