// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package git wraps git shell-outs behind the GitOps interface so the
// execution and ai_review steps can be tested with a counterfeiter mock.
// Implementations are responsible for assembling argv slices, capturing
// stderr, and wrapping errors via bborbe/errors. They MUST NOT use sh -c
// or any shell-interpolated form — argv only.
//
// Auth model: HTTPS clones with a GitHub token are handled by URL
// transformation at the call site (cloneURL → https://x-access-token:<tok>@github.com/...).
// The package itself takes the transformed URL — it does not know about
// tokens directly.
//
// Deliberately NO Tag method exists on this interface (design § 7.0):
// versioning + tagging is the github-releaser agent's job on merge. The
// only push helper hardcodes --no-follow-tags and pushes a single branch
// ref — a tag physically cannot leak from this agent.
package git

import "context"

//counterfeiter:generate -o ../../mocks/git_ops.go --fake-name GitOps . GitOps

// GitOps is the seam between the steps and the git binary.
//
// All methods are context-aware — callers can cancel mid-operation.
// workdir is the absolute path to the checkout (created and owned by the
// caller; the package does not manage workdir lifecycle).
//
//nolint:revive // GitOps is the design-required name; the seam name is frozen
type GitOps interface {
	// CloneAtRef shells out `git clone <cloneURL> <workdir>` (full history —
	// the ref may be an arbitrary SHA) and checks out ref. ref may be a
	// commit SHA or a branch name (branch names resolve via checkout DWIM
	// against origin). cloneURL MUST already include any auth token.
	CloneAtRef(ctx context.Context, cloneURL, ref, workdir string) error

	// SwitchNewBranch creates and switches to a new local branch
	// (git switch -c <branch>).
	SwitchNewBranch(ctx context.Context, workdir, branch string) error

	// ChangedFiles returns the repo-relative paths of all uncommitted
	// changes (staged, unstaged, and untracked) via `git status --porcelain`.
	// The execution step uses it to build the explicit commit pathspec and
	// to run the workflow-edit guard BEFORE anything is committed.
	ChangedFiles(ctx context.Context, workdir string) ([]string, error)

	// Commit stages paths (relative to workdir) via explicit `git add -- <paths>`
	// and creates a commit with the bot identity. Returns the short SHA
	// (7 chars) of the new commit. The bot identity is set per-invocation
	// via -c user.name / -c user.email — never writes to the global gitconfig.
	Commit(ctx context.Context, workdir, message string, paths ...string) (sha string, err error)

	// CommittedFiles returns the repo-relative paths changed by the HEAD
	// commit (git diff-tree --no-commit-id --name-only -r HEAD). The
	// execution step uses it as a pre-push guard.
	CommittedFiles(ctx context.Context, workdir string) ([]string, error)

	// Push pushes the current HEAD to origin as refs/heads/<branch>,
	// hardcoding --no-follow-tags so a local tag can never reach origin.
	Push(ctx context.Context, workdir, branch string) error

	// LsRemoteTags shells out `git ls-remote --tags <cloneURL>` and returns
	// all SHAs sitting at tag refs (both tag-object SHAs and dereferenced
	// ^{} commit SHAs). The ai_review step checks that no branch commit
	// carries a tag. cloneURL MUST already include any auth token.
	LsRemoteTags(ctx context.Context, cloneURL string) ([]string, error)

	// RevList returns the commit SHAs on HEAD that are not reachable from
	// base (git rev-list <base>..HEAD) — the branch's own commits.
	RevList(ctx context.Context, workdir, base string) ([]string, error)

	// ShowFile returns the content of path at the given ref
	// (git show <ref>:<path>) — e.g. origin/master:CHANGELOG.md.
	ShowFile(ctx context.Context, workdir, ref, path string) ([]byte, error)
}
