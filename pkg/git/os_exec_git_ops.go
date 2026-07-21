// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// BotIdentity holds the commit author/committer identity. Hardcoded
// intentionally: the only consumer is github-update-go-agent, and a single
// value is the contract (mirrors github-releaser-agent).
type BotIdentity struct {
	Name  string
	Email string
}

// DefaultBotIdentity returns the fleet-standard bot identity. The osExecGitOps
// struct reads this internally on every Commit — there is no override path.
// Exposed publicly for test assertions.
func DefaultBotIdentity() BotIdentity {
	return BotIdentity{
		Name:  "Benjamin Borbe",
		Email: "bborbe@users.noreply.github.com",
	}
}

// NewOSExecGitOps returns a GitOps implementation that shells out to the
// git binary via os/exec. Zero-arg: the bot identity is constant via
// DefaultBotIdentity().
func NewOSExecGitOps() GitOps {
	return &osExecGitOps{}
}

type osExecGitOps struct{}

// cmdEnv returns the env allowlist for git subprocesses: HOME (for ~/.gitconfig
// fallback) + PATH (to resolve git). Strict allowlist prevents pod-level
// secrets from leaking. Mirrors github-releaser-agent's osExecGitOps.cmdEnv.
func (g *osExecGitOps) cmdEnv() []string {
	return []string{
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
}

func (g *osExecGitOps) CloneAtRef(ctx context.Context, cloneURL, ref, workdir string) error {
	// git clone <cloneURL> <workdir> — FULL clone (no --depth): the trigger
	// ref is an arbitrary commit SHA that a shallow default-branch clone may
	// not contain, and ai_review needs origin/master for the CHANGELOG
	// comparison plus rev-list history.
	// #nosec G204 -- cloneURL constructed in caller from validated frontmatter; workdir is os.TempDir-rooted
	cmd := exec.CommandContext(ctx, "git", "clone", cloneURL, workdir)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(ctx, "git clone: %s", redactToken(strings.TrimSpace(stderr.String())))
	}

	// git -C <workdir> checkout <ref> — ref may be a SHA (detached HEAD) or
	// a branch name (DWIM creates a local tracking branch from origin/<ref>).
	// #nosec G204 -- workdir is os.TempDir-rooted; ref comes from validated frontmatter / deterministic branch name
	checkout := exec.CommandContext(ctx, "git", "-C", workdir, "checkout", ref)
	checkout.Env = g.cmdEnv()
	var checkoutStderr bytes.Buffer
	checkout.Stderr = &checkoutStderr
	if err := checkout.Run(); err != nil {
		return errors.Errorf(
			ctx,
			"git checkout %s: %s",
			ref,
			redactToken(strings.TrimSpace(checkoutStderr.String())),
		)
	}
	glog.V(2).Infof("git clone+checkout succeeded: ref=%s workdir=%s", ref, workdir)
	return nil
}

func (g *osExecGitOps) SwitchNewBranch(ctx context.Context, workdir, branch string) error {
	// git -C <workdir> switch -c <branch>
	// #nosec G204 -- workdir is os.TempDir-rooted; branch is the deterministic fix/update-go-<sha:7> name
	cmd := exec.CommandContext(ctx, "git", "-C", workdir, "switch", "-c", branch)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(
			ctx,
			"git switch -c %s: %s",
			branch,
			strings.TrimSpace(stderr.String()),
		)
	}
	return nil
}

// ChangedFiles returns all uncommitted repo-relative paths via
// `git status --porcelain` (staged + unstaged + untracked).
func (g *osExecGitOps) ChangedFiles(ctx context.Context, workdir string) ([]string, error) {
	// git -C <workdir> status --porcelain
	// #nosec G204 -- workdir is os.TempDir-rooted; all other args are constants
	out, err := exec.CommandContext(ctx, "git", "-C", workdir, "status", "--porcelain").Output()
	if err != nil {
		return nil, errors.Wrap(ctx, err, "git status --porcelain")
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain format: XY <path> (or XY <old> -> <new> for renames).
		path := strings.TrimSpace(line[3:])
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		path = strings.Trim(path, `"`)
		if path != "" {
			files = append(files, path)
		}
	}
	glog.V(2).Infof("git status --porcelain: files=%v", files)
	return files, nil
}

func (g *osExecGitOps) Commit(
	ctx context.Context,
	workdir, message string,
	paths ...string,
) (string, error) {
	// git -C <workdir> add -- <paths...> — explicit pathspec only, never -A.
	if len(paths) > 0 {
		addArgs := append([]string{"-C", workdir, "add", "--"}, paths...)
		// #nosec G204 -- workdir is os.TempDir-rooted; paths come from the execution step's changed-files guard
		if out, err := exec.CommandContext(ctx, "git", addArgs...).CombinedOutput(); err != nil {
			return "", errors.Errorf(ctx, "git add: %s", strings.TrimSpace(string(out)))
		}
	}

	// git -C <workdir> -c user.name=<name> -c user.email=<email> commit -m <message>
	id := DefaultBotIdentity()
	commitArgs := []string{
		"-C", workdir,
		"-c", "user.name=" + id.Name,
		"-c", "user.email=" + id.Email,
		"commit",
		"-m", message,
	}
	// #nosec G204 -- workdir is os.TempDir-rooted; identity is the bot constant; message comes from execution step
	if out, err := exec.CommandContext(ctx, "git", commitArgs...).CombinedOutput(); err != nil {
		return "", errors.Errorf(ctx, "git commit: %s", strings.TrimSpace(string(out)))
	}

	// git -C <workdir> rev-parse --short HEAD → short SHA
	// #nosec G204 -- workdir is os.TempDir-rooted; args are hardcoded
	shaBytes, err := exec.CommandContext(ctx, "git", "-C", workdir, "rev-parse", "--short", "HEAD").
		Output()
	if err != nil {
		return "", errors.Wrap(ctx, err, "git rev-parse HEAD")
	}
	return strings.TrimSpace(string(shaBytes)), nil
}

// CommittedFiles returns the repo-relative paths changed by the HEAD commit.
func (g *osExecGitOps) CommittedFiles(ctx context.Context, workdir string) ([]string, error) {
	// git -C <workdir> diff-tree --no-commit-id --name-only -r HEAD
	// #nosec G204 -- workdir is os.TempDir-rooted; all other args are constants
	out, err := exec.CommandContext(
		ctx, "git", "-C", workdir, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD",
	).Output()
	if err != nil {
		return nil, errors.Wrap(ctx, err, "git diff-tree HEAD")
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	glog.V(2).Infof("git diff-tree HEAD: files=%v", files)
	return files, nil
}

func (g *osExecGitOps) Push(ctx context.Context, workdir, branch string) error {
	// git -C <workdir> push --no-follow-tags origin HEAD:refs/heads/<branch>
	// --no-follow-tags is hardcoded: a local tag must NEVER reach origin
	// (versioning + tagging is the release agent's job on merge).
	// No --force / --force-with-lease — non-fast-forward maps to retry, not overwrite.
	// #nosec G204 -- workdir is os.TempDir-rooted; branch is the deterministic fix/update-go-<sha:7> name
	cmd := exec.CommandContext(
		ctx,
		"git", "-C", workdir,
		"push", "--no-follow-tags", "origin", "HEAD:refs/heads/"+branch,
	)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return errors.Errorf(ctx, "git push: %s", redactToken(strings.TrimSpace(stderr.String())))
	}
	return nil
}

// LsRemoteTags returns every SHA the remote holds at a tag ref — both the
// tag-object SHA and the dereferenced ^{} commit SHA lines.
func (g *osExecGitOps) LsRemoteTags(ctx context.Context, cloneURL string) ([]string, error) {
	// git ls-remote --tags <cloneURL>
	// #nosec G204 -- cloneURL is authed by caller from validated frontmatter
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--tags", cloneURL)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.Errorf(
			ctx,
			"git ls-remote --tags: %s",
			redactToken(strings.TrimSpace(stderr.String())),
		)
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			shas = append(shas, fields[0])
		}
	}
	glog.V(2).Infof("git ls-remote --tags: %d tag shas", len(shas))
	return shas, nil
}

// RevList returns the commit SHAs on HEAD that are not reachable from base.
func (g *osExecGitOps) RevList(ctx context.Context, workdir, base string) ([]string, error) {
	// git -C <workdir> rev-list <base>..HEAD
	// #nosec G204 -- workdir is os.TempDir-rooted; base comes from validated frontmatter ref
	out, err := exec.CommandContext(ctx, "git", "-C", workdir, "rev-list", base+"..HEAD").Output()
	if err != nil {
		return nil, errors.Wrapf(ctx, err, "git rev-list %s..HEAD", base)
	}
	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			shas = append(shas, trimmed)
		}
	}
	return shas, nil
}

// ShowFile returns the content of path at the given ref.
func (g *osExecGitOps) ShowFile(
	ctx context.Context,
	workdir, ref, path string,
) ([]byte, error) {
	// git -C <workdir> show <ref>:<path>
	// #nosec G204 -- workdir is os.TempDir-rooted; ref/path come from the review step's constants
	cmd := exec.CommandContext(ctx, "git", "-C", workdir, "show", ref+":"+path)
	cmd.Env = g.cmdEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.Errorf(
			ctx,
			"git show %s:%s: %s",
			ref, path, strings.TrimSpace(stderr.String()),
		)
	}
	return out, nil
}

// redactToken strips x-access-token:<TOK>@ patterns from stderr to prevent
// GH_TOKEN from landing in error logs. Git can echo the URL with embedded
// credentials on auth/clone failures. Apply to ALL stderr that involves the
// authed URL before it gets wrapped into errors.
func redactToken(s string) string {
	return tokenURLRegexp.ReplaceAllString(s, "x-access-token:[REDACTED]@")
}

// RedactToken exposes the unexported redactToken helper for callers outside
// this package that need to log wrapped err.Error() strings through the same
// redaction. Behavior is identical to redactToken.
func RedactToken(s string) string {
	return redactToken(s)
}

// tokenURLRegexp is compiled once at package init so the hot path does not
// recompile per call.
var tokenURLRegexp = regexp.MustCompile(`x-access-token:[^@\s]+@`)
