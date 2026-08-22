// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

//counterfeiter:generate -o ../mocks/gh_cli.go --fake-name GhCli . GhCli

// GhCli is the seam between the steps and the gh binary. Three methods
// cover the agent's entire PR surface: create a PR at a configurable target
// (draft or ready-for-review), adopt an already-open PR by head branch
// (crash-window replay guard), and view PR state for the ai_review checks.
//
// The creation target is chosen per deployment; the agent itself only opens
// PRs and never changes their draft/ready state after creation. Deliberately
// absent: no Ready, no Merge — the agent never flips the draft flag of an
// already-open pull request and never merges; the human does.
type GhCli interface {
	// CreatePR opens a pull request from head against base, running gh
	// inside workdir so the repo is inferred from the git remote. target
	// selects draft or ready-for-review at creation time. label, when
	// non-empty, is added to the PR at creation (e.g. "auto-merge" to opt
	// the PR into GitHub-native auto-merge). Returns the PR URL.
	CreatePR(
		ctx context.Context,
		workdir, base, head, title, body string,
		target PRTarget,
		label string,
	) (string, error)

	// FindOpenPRByHead returns the URL of an open PR whose head is the
	// given branch, or "" when none exists. repo is "owner/name".
	FindOpenPRByHead(ctx context.Context, repo, head string) (string, error)

	// ViewPR returns the state (e.g. "OPEN", "MERGED", "CLOSED") and
	// draft flag of the PR identified by URL.
	ViewPR(ctx context.Context, prURL string) (state string, isDraft bool, err error)

	// FetchFailedLogs returns the failed-step log tail for the latest
	// failing run of the episode SHA on the repo, via `gh run view
	// --log-failed`. Returns "" when no failing run exists for the episode
	// (the caller treats it as "no log evidence"). Used by the build-fix
	// planning step to give the diagnosis LLM concrete failure evidence.
	FetchFailedLogs(ctx context.Context, repo, episodeSHA string) (string, error)
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

// prCreateArgs builds the argv for `gh pr create`. --draft is included
// only when the target is draft; a ready target omits the flag entirely.
// label, when non-empty, is appended as `--label <label>` so the PR carries
// the opt-in marker from birth (e.g. `auto-merge`).
func prCreateArgs(base, head, title, body string, target PRTarget, label string) []string {
	args := []string{"pr", "create"}
	if target.IsDraft() {
		args = append(args, "--draft")
	}
	args = append(args, "--base", base, "--head", head, "--title", title, "--body", body)
	if label != "" {
		args = append(args, "--label", label)
	}
	return args
}

// isMissingLabelError reports whether gh refused to create the PR because the
// requested label does not exist in the repository. gh's message is
// "could not add label: '<name>' not found".
func isMissingLabelError(output string) bool {
	return strings.Contains(output, "could not add label") &&
		strings.Contains(output, "not found")
}

// runPRCreate shells out to `gh pr create` once and returns its combined
// output alongside the run error, so CreatePR can inspect the failure and
// decide whether a label-free retry is warranted.
func (g *osExecGhCli) runPRCreate(
	ctx context.Context,
	workdir, base, head, title, body string,
	target PRTarget,
	label string,
) ([]byte, error) {
	// #nosec G204 -- binary is hardcoded gh; workdir is os.TempDir-rooted; head is the deterministic branch name
	cmd := exec.CommandContext(ctx, "gh", prCreateArgs(base, head, title, body, target, label)...)
	cmd.Dir = workdir
	cmd.Env = g.cmdEnv()
	return cmd.CombinedOutput()
}

func (g *osExecGhCli) CreatePR(
	ctx context.Context,
	workdir, base, head, title, body string,
	target PRTarget,
	label string,
) (string, error) {
	out, err := g.runPRCreate(ctx, workdir, base, head, title, body, target, label)
	if err != nil && label != "" && isMissingLabelError(string(out)) {
		// The label is an opt-in marker, not part of the update. gh validates
		// labels before creating anything, so nothing was opened — retrying
		// without the label cannot duplicate a PR. Losing the auto-merge
		// opt-in is strictly better than losing the PR: on 2026-08-19 every
		// repo in the deps sweep failed here with "could not add label:
		// 'auto-merge' not found" because no repo defined that label, and the
		// whole fleet stopped producing PRs.
		glog.V(0).Infof(
			"gh pr create: label %q does not exist in this repo — opening the PR without it "+
				"(auto-merge opt-in skipped); create the label to enable it",
			label,
		)
		out, err = g.runPRCreate(ctx, workdir, base, head, title, body, target, "")
	}
	if err != nil {
		return "", errors.Errorf(
			ctx,
			"gh pr create (target=%s): %s",
			target,
			strings.TrimSpace(string(out)),
		)
	}
	// gh prints the PR URL as the last non-empty stdout line.
	url := lastNonEmptyLine(string(out))
	glog.V(2).Infof("gh pr create (target=%s) succeeded: url=%s head=%s", target, url, head)
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

// FetchFailedLogs returns the failed-step log tail for the latest failing
// run of the episode SHA. It first finds the failing run id via
// `gh run list --commit <sha> --json databaseId,conclusion` (lowest id whose
// conclusion is failure), then fetches `gh run view <id> --log-failed` and
// bounds the output to a diagnosis-sized tail. Returns "" when no failing
// run exists for the episode — the caller treats that as "no log evidence".
func (g *osExecGhCli) FetchFailedLogs(
	ctx context.Context,
	repo, episodeSHA string,
) (string, error) {
	// #nosec G204 -- binary is hardcoded gh; repo + episodeSHA come from task frontmatter
	listCmd := exec.CommandContext(
		ctx,
		"gh", "run", "list",
		"--repo", repo,
		"--commit", episodeSHA,
		"--json", "databaseId,conclusion",
	)
	listCmd.Env = g.cmdEnv()
	listOut, err := listCmd.Output()
	if err != nil {
		// gh exits non-zero when no runs match the commit — treat as "no evidence".
		return "", nil
	}
	var runs []struct {
		DatabaseID int64  `json:"databaseId"`
		Conclusion string `json:"conclusion"`
	}
	if err := json.Unmarshal(listOut, &runs); err != nil {
		return "", errors.Wrap(ctx, err, "parse gh run list output")
	}
	var failingID int64
	for _, r := range runs {
		if r.Conclusion != "success" && r.Conclusion != "skipped" {
			failingID = r.DatabaseID
			break
		}
	}
	if failingID == 0 {
		return "", nil
	}
	// #nosec G204 -- binary is hardcoded gh; failingID comes from gh run list output; repo from validated frontmatter
	logCmd := exec.CommandContext(
		ctx,
		"gh",
		"run",
		"view",
		fmt.Sprintf("%d", failingID),
		"--repo",
		repo,
		"--log-failed",
	)
	logCmd.Env = g.cmdEnv()
	logOut, err := logCmd.Output()
	if err != nil {
		return "", errors.Wrapf(ctx, err, "gh run view %d --log-failed", failingID)
	}
	return truncateToLines(string(logOut), 200), nil
}

// truncateToLines bounds s to at most n lines (diagnosis-sized log tail).
func truncateToLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(s)
	}
	return strings.Join(lines[:n], "\n") + "\n... (truncated)"
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
