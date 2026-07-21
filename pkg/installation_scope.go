// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/golang/glog"
)

// ScopeVerdict is the outcome of an installation-scope check.
type ScopeVerdict string

const (
	// ScopeAllowed — the repo is in the App installation's repository list.
	ScopeAllowed ScopeVerdict = "allowed"
	// ScopeDenied — the installation token definitively cannot access the repo.
	ScopeDenied ScopeVerdict = "denied"
	// ScopeUnknown — the check could not be performed (PAT fallback, API
	// error). Callers proceed; a real permission gap still fails at push.
	ScopeUnknown ScopeVerdict = "unknown"
)

//counterfeiter:generate -o ../mocks/installation_scope.go --fake-name InstallationScope . InstallationScope

// InstallationScope answers whether the GitHub App installation backing the
// agent's token covers a repo. The App installation's repository selection IS
// the fleet's per-stage allowlist (design § 7.2): a repo outside it must park
// in planning, before any clone/update work — not after ten minutes of work
// at the push step.
type InstallationScope interface {
	// Allows reports whether repo ("owner/name") is covered by the token's
	// installation. Unknown means the check itself was impossible — never
	// treat that as denial.
	Allows(ctx context.Context, repo string) ScopeVerdict
}

// NewGhInstallationScope returns an InstallationScope that shells out to
// `gh api /installation/repositories` with the agent's token. With an App
// installation token the endpoint lists exactly the accessible repos; with
// the local GH_TOKEN PAT fallback it 403s, which maps to ScopeUnknown
// (local runs are operator-supervised — no allowlist to enforce).
func NewGhInstallationScope(ghToken string) InstallationScope {
	return &ghInstallationScope{ghToken: ghToken}
}

type ghInstallationScope struct {
	ghToken string
}

// Allows implements InstallationScope via one paginated listing call.
func (g *ghInstallationScope) Allows(ctx context.Context, repo string) ScopeVerdict {
	cmd := exec.CommandContext(ctx,
		"gh", "api", "/installation/repositories",
		"--paginate", "--jq", ".repositories[].full_name",
	)
	env := []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	if g.ghToken != "" {
		env = append(env, "GH_TOKEN="+g.ghToken)
	}
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		glog.V(2).Infof("installation-scope: listing failed (%v) — verdict unknown", err)
		return ScopeUnknown
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.EqualFold(strings.TrimSpace(line), repo) {
			return ScopeAllowed
		}
	}
	glog.V(2).Infof("installation-scope: repo %s not in installation list — verdict denied", repo)
	return ScopeDenied
}
