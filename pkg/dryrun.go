// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	"github.com/bborbe/errors"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

// DryRunResolve resolves the default branch of repo ("owner/name") and returns
// its name and the commit SHA at its tip. It is the whole of the --dry-run
// path: it performs the same `git ls-remote --symref HEAD` resolution the real
// run starts with, then returns — no clone, no branch, no commit, no push, no
// PR. It exists so a repo whose default branch is not `master` can be proven
// updatable without writing anything.
//
// ghToken is the operator's raw GitHub token; empty is allowed for a public
// repo (anonymous resolution).
func DryRunResolve(
	ctx context.Context,
	ops git.GitOps,
	ghToken, repo string,
) (branch, headSHA string, err error) {
	owner, name, ok := parseOwnerRepo(repo)
	if !ok {
		return "", "", errors.Errorf(ctx, "dry-run: malformed repo %q (want owner/name)", repo)
	}
	cloneURL := "https://github.com/" + owner + "/" + name + ".git"
	authedURL := injectToken(normalizeCloneURLToHTTPS(cloneURL), ghToken)
	return ops.ResolveDefaultBranch(ctx, authedURL)
}
