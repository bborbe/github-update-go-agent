// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

var _ = Describe("DefaultBotIdentity", func() {
	It("returns the fleet-standard identity", func() {
		id := git.DefaultBotIdentity()
		Expect(id.Name).To(Equal("Benjamin Borbe"))
		Expect(id.Email).To(Equal("bborbe@users.noreply.github.com"))
	})
})

var _ = Describe("NewOSExecGitOps", func() {
	It("returns a non-nil GitOps", func() {
		Expect(git.NewOSExecGitOps()).NotTo(BeNil())
	})
})

// Boundary-crossing integration tests — exercise the real `git` binary
// against local repos. These prove clone-at-SHA, the -c identity injection,
// and the --no-follow-tags push contract through the actual shell-out, not
// just via mocks. Skip on systems without git. Per-test tempdirs for
// isolation (pattern copied from github-releaser-agent).
var _ = Describe("osExecGitOps boundary contracts", func() {
	var (
		ctx     context.Context
		tmp     string
		source  string
		ops     git.GitOps
		gitRun  func(dir string, args ...string) string
		firstSHA string
	)

	BeforeEach(func() {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git binary not available")
		}
		ctx = context.Background()
		var err error
		tmp, err = os.MkdirTemp("", "github-update-go-git-test-*")
		Expect(err).NotTo(HaveOccurred())

		gitRun = func(dir string, args ...string) string {
			full := append([]string{
				"-C", dir,
				"-c", "user.name=Test",
				"-c", "user.email=test@example.com",
			}, args...)
			out, err := exec.Command("git", full...).CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "git %v: %s", args, string(out))
			return strings.TrimSpace(string(out))
		}

		// Source repo with two commits so clone-at-SHA is provable.
		source = filepath.Join(tmp, "source")
		Expect(os.MkdirAll(source, 0o750)).To(Succeed())
		gitRun(source, "init", "-b", "master")
		Expect(os.WriteFile(filepath.Join(source, "MARKER.txt"), []byte("one\n"), 0o600)).
			To(Succeed())
		gitRun(source, "add", "MARKER.txt")
		gitRun(source, "commit", "-m", "first")
		firstSHA = gitRun(source, "rev-parse", "HEAD")
		Expect(os.WriteFile(filepath.Join(source, "MARKER.txt"), []byte("two\n"), 0o600)).
			To(Succeed())
		gitRun(source, "add", "MARKER.txt")
		gitRun(source, "commit", "-m", "second")

		ops = git.NewOSExecGitOps()
	})

	AfterEach(func() {
		os.RemoveAll(tmp)
	})

	It("CloneAtRef checks out the given SHA, not the branch tip", func() {
		dest := filepath.Join(tmp, "cloned-at-sha")
		Expect(ops.CloneAtRef(ctx, source, firstSHA, dest)).To(Succeed())
		got, err := os.ReadFile(filepath.Join(dest, "MARKER.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(got)).To(Equal("one\n"))
	})

	It("SwitchNewBranch creates and switches to the branch", func() {
		dest := filepath.Join(tmp, "cloned-branch")
		Expect(ops.CloneAtRef(ctx, source, "master", dest)).To(Succeed())
		Expect(ops.SwitchNewBranch(ctx, dest, "fix/update-go-1234567")).To(Succeed())
		branch := gitRun(dest, "branch", "--show-current")
		Expect(branch).To(Equal("fix/update-go-1234567"))
	})

	It("ChangedFiles reports staged, unstaged, and untracked paths", func() {
		dest := filepath.Join(tmp, "cloned-changed")
		Expect(ops.CloneAtRef(ctx, source, "master", dest)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "MARKER.txt"), []byte("edit\n"), 0o600)).
			To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "NEW.txt"), []byte("new\n"), 0o600)).
			To(Succeed())
		files, err := ops.ChangedFiles(ctx, dest)
		Expect(err).NotTo(HaveOccurred())
		Expect(files).To(ConsistOf("MARKER.txt", "NEW.txt"))
	})

	It("Commit stages the explicit pathspec with the bot identity and CommittedFiles matches", func() {
		dest := filepath.Join(tmp, "cloned-commit")
		Expect(ops.CloneAtRef(ctx, source, "master", dest)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "MARKER.txt"), []byte("edit\n"), 0o600)).
			To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "UNRELATED.txt"), []byte("skip\n"), 0o600)).
			To(Succeed())

		sha, err := ops.Commit(ctx, dest, "update go module dependencies", "MARKER.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(sha).NotTo(BeEmpty())

		author := gitRun(dest, "log", "-1", "--format=%an <%ae>")
		Expect(author).To(Equal("Benjamin Borbe <bborbe@users.noreply.github.com>"))

		committed, err := ops.CommittedFiles(ctx, dest)
		Expect(err).NotTo(HaveOccurred())
		Expect(committed).To(Equal([]string{"MARKER.txt"}))
	})

	It("Push pushes HEAD to the branch ref and NEVER pushes local tags (--no-follow-tags)", func() {
		// Bare origin so push has a target.
		origin := filepath.Join(tmp, "origin.git")
		Expect(os.MkdirAll(origin, 0o750)).To(Succeed())
		out, err := exec.Command("git", "-C", origin, "init", "--bare", "-b", "master").
			CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), string(out))

		dest := filepath.Join(tmp, "cloned-push")
		Expect(ops.CloneAtRef(ctx, source, "master", dest)).To(Succeed())
		gitRun(dest, "remote", "set-url", "origin", origin)
		Expect(ops.SwitchNewBranch(ctx, dest, "fix/update-go-abcdef0")).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "MARKER.txt"), []byte("push\n"), 0o600)).
			To(Succeed())
		_, err = ops.Commit(ctx, dest, "update go module dependencies", "MARKER.txt")
		Expect(err).NotTo(HaveOccurred())

		// A stray local tag must NOT reach origin.
		gitRun(dest, "tag", "v9.9.9")

		Expect(ops.Push(ctx, dest, "fix/update-go-abcdef0")).To(Succeed())

		branches, err := exec.Command("git", "-C", origin, "branch", "--list").CombinedOutput()
		Expect(err).NotTo(HaveOccurred())
		Expect(string(branches)).To(ContainSubstring("fix/update-go-abcdef0"))

		tags, err := exec.Command("git", "-C", origin, "tag", "--list").CombinedOutput()
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(tags))).To(BeEmpty(), "local tag leaked to origin")
	})

	It("LsRemoteTags returns the SHAs at the remote's tag refs", func() {
		tagSHA := gitRun(source, "rev-parse", "HEAD")
		gitRun(source, "tag", "v1.0.0")
		shas, err := ops.LsRemoteTags(ctx, source)
		Expect(err).NotTo(HaveOccurred())
		Expect(shas).To(ContainElement(tagSHA))
	})

	It("LsRemoteTags returns empty for a remote without tags", func() {
		shas, err := ops.LsRemoteTags(ctx, source)
		Expect(err).NotTo(HaveOccurred())
		Expect(shas).To(BeEmpty())
	})

	It("RevList returns only the branch's own commits", func() {
		dest := filepath.Join(tmp, "cloned-revlist")
		Expect(ops.CloneAtRef(ctx, source, "master", dest)).To(Succeed())
		base := gitRun(dest, "rev-parse", "HEAD")
		Expect(ops.SwitchNewBranch(ctx, dest, "fix/update-go-fffffff")).To(Succeed())
		Expect(os.WriteFile(filepath.Join(dest, "MARKER.txt"), []byte("three\n"), 0o600)).
			To(Succeed())
		newSHA, err := ops.Commit(ctx, dest, "third", "MARKER.txt")
		Expect(err).NotTo(HaveOccurred())

		shas, err := ops.RevList(ctx, dest, base)
		Expect(err).NotTo(HaveOccurred())
		Expect(shas).To(HaveLen(1))
		Expect(shas[0]).To(HavePrefix(newSHA))
	})

	It("ShowFile reads a file at a ref without touching the worktree", func() {
		dest := filepath.Join(tmp, "cloned-show")
		Expect(ops.CloneAtRef(ctx, source, "master", dest)).To(Succeed())
		content, err := ops.ShowFile(ctx, dest, firstSHA, "MARKER.txt")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("one\n"))
	})
})
