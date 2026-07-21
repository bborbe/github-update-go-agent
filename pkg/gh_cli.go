// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

//counterfeiter:generate -o ../mocks/gh_cli.go --fake-name GhCli . GhCli

// GhCli is the seam between the steps and the gh binary. Three methods
// cover the agent's entire PR surface: create a DRAFT PR, adopt an
// already-open PR by head branch (crash-window replay guard), and view
// PR state for the ai_review checks.
//
// Deliberately absent (design § 7.0): no Ready, no Merge — the agent
// never flips a draft and never merges; the human does.
type GhCli interface {
	// CreateDraftPR opens a DRAFT pull request from head against base,
	// running gh inside workdir so the repo is inferred from the git
	// remote. Returns the PR URL.
	CreateDraftPR(ctx context.Context, workdir, base, head, title, body string) (string, error)

	// FindOpenPRByHead returns the URL of an open PR whose head is the
	// given branch, or "" when none exists. repo is "owner/name".
	FindOpenPRByHead(ctx context.Context, repo, head string) (string, error)

	// ViewPR returns the state (e.g. "OPEN", "MERGED", "CLOSED") and
	// draft flag of the PR identified by URL.
	ViewPR(ctx context.Context, prURL string) (state string, isDraft bool, err error)
}

// NewOSExecGhCli returns a GhCli implementation that shells out to the gh
// binary with a minimal allowlisted env (GH_TOKEN, HOME, PATH).
func NewOSExecGhCli(ghToken string) GhCli {
	return &osExecGhCli{ghToken: ghToken}
}

type osExecGhCli struct {
	ghToken string
}

// cmdEnv returns the env allowlist for gh subprocesses. gh needs HOME to
// locate ~/.config/gh and PATH to resolve git; GH_TOKEN carries the
// credential. Strict allowlist prevents pod-level secrets from leaking.
func (g *osExecGhCli) cmdEnv() []string {
	env := []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	if g.ghToken != "" {
		env = append(env, "GH_TOKEN="+g.ghToken)
	}
	return env
}

func (g *osExecGhCli) CreateDraftPR(
	ctx context.Context,
	workdir, base, head, title, body string,
) (string, error) {
	// gh pr create --draft --base <base> --head <head> --title <title> --body <body>
	// --draft is hardcoded: the agent opens drafts only; the human promotes.
	// #nosec G204 -- binary is hardcoded gh; workdir is os.TempDir-rooted; head is the deterministic branch name
	cmd := exec.CommandContext(
		ctx,
		"gh", "pr", "create",
		"--draft",
		"--base", base,
		"--head", head,
		"--title", title,
		"--body", body,
	)
	cmd.Dir = workdir
	cmd.Env = g.cmdEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", errors.Errorf(
			ctx,
			"gh pr create --draft: %s",
			strings.TrimSpace(string(out)),
		)
	}
	// gh prints the PR URL as the last non-empty stdout line.
	url := lastNonEmptyLine(string(out))
	glog.V(2).Infof("gh pr create --draft succeeded: url=%s head=%s", url, head)
	return url, nil
}

func (g *osExecGhCli) FindOpenPRByHead(
	ctx context.Context,
	repo, head string,
) (string, error) {
	// gh pr list --repo <repo> --head <head> --state open --json url
	// #nosec G204 -- binary is hardcoded gh; repo comes from validated frontmatter; head is deterministic
	cmd := exec.CommandContext(
		ctx,
		"gh", "pr", "list",
		"--repo", repo,
		"--head", head,
		"--state", "open",
		"--json", "url",
	)
	cmd.Env = g.cmdEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", errors.Wrapf(ctx, err, "gh pr list --head %s", head)
	}
	var prs []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return "", errors.Wrap(ctx, err, "parse gh pr list output")
	}
	if len(prs) == 0 {
		return "", nil
	}
	return prs[0].URL, nil
}

func (g *osExecGhCli) ViewPR(
	ctx context.Context,
	prURL string,
) (string, bool, error) {
	// gh pr view <url> --json state,isDraft
	// #nosec G204 -- binary is hardcoded gh; prURL comes from the agent's own ## Result section
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", prURL, "--json", "state,isDraft")
	cmd.Env = g.cmdEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false, errors.Wrapf(ctx, err, "gh pr view %s", prURL)
	}
	var pr struct {
		State   string `json:"state"`
		IsDraft bool   `json:"isDraft"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return "", false, errors.Wrap(ctx, err, "parse gh pr view output")
	}
	return pr.State, pr.IsDraft, nil
}

// lastNonEmptyLine returns the last non-empty line of s (gh prints the PR
// URL last, after any informational lines).
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if trimmed := strings.TrimSpace(lines[i]); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
