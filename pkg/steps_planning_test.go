// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

const planningTaskMD = `---
task_type: github-update-go
assignee: github-update-go-agent
phase: planning
status: in_progress
repo: bborbe/demo
clone_url: git@github.com:bborbe/demo.git
ref: 6d1f27fabcdef12345678901234567890abcdef1
task_identifier: test-task-1
---

Update Go bborbe/demo
`

// fixtureMakefile defines check + vulncheck with @echo recipes emitting
// canned scanner output in the shapes the Go parser recognizes: an osv
// row and a govulncheck row under check, a second govulncheck row under
// vulncheck.
var fixtureMakefile = ".PHONY: check vulncheck\n" +
	"check:\n" +
	"\t@echo 'GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6'\n" +
	"\t@echo 'GO-2026-5932\tgolang.org/x/crypto/openpgp@v0.0.0-20241113183425-a8a1ce24caf7 -> v0.38.0\tOpenPGP default weak'\n" +
	"vulncheck:\n" +
	"\t@echo 'CVE-2026-9999\tgolang.org/x/net@v0.32.0 -> v0.36.0\tsummary'\n"

// fixtureMakefilePrefixCollision adds a GO-2026-5026 row whose 5-digit tail
// shares a prefix with the fabricated GO-2026-50260.
var fixtureMakefilePrefixCollision = ".PHONY: check vulncheck\n" +
	"check:\n" +
	"\t@echo 'GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6'\n" +
	"\t@echo 'GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6'\n" +
	"\t@echo 'GO-2026-5932\tgolang.org/x/crypto/openpgp@v0.0.0-20241113183425-a8a1ce24caf7 -> v0.38.0\tOpenPGP default weak'\n" +
	"vulncheck:\n" +
	"\t@echo 'CVE-2026-9999\tgolang.org/x/net@v0.32.0 -> v0.36.0\tsummary'\n"

// fixtureMakefileEmpty defines the same gate targets but their recipes emit
// no scanner findings (exit 0).
var fixtureMakefileEmpty = ".PHONY: check vulncheck\n" +
	"check:\n" +
	"\t@:\n" +
	"vulncheck:\n" +
	"\t@:\n"

// fixtureMakefileBroken defines a gate target that fails with output that
// carries no advisory IDs.
var fixtureMakefileBroken = ".PHONY: check\n" +
	"check:\n" +
	"\t@echo 'make: something broken' >&2; exit 1\n"

var _ = Describe("PlanningStep", func() {
	var (
		ctx    context.Context
		runner *mocks.ClaudeRunnerMock
		ops    *mocks.GitOps
		scope  *mocks.InstallationScope
		step   agentlib.Step
		md     *agentlib.Markdown
	)

	// setupFixture makes CloneAtRef create the workdir and write the given
	// Makefile, mirroring a real clone. setupWorkdir removes the stale dir
	// inside Run before the stub runs, so the fixture is written after that
	// cleanup and the real osExecGateRunner can run `make -C <workdir>`.
	setupFixture := func(makefile string) {
		ops.CloneAtRefStub = func(ctx context.Context, url, ref, workdir string) error {
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(workdir, "Makefile"), []byte(makefile), 0o644)
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		ops = &mocks.GitOps{}
		scope = &mocks.InstallationScope{}
		scope.AllowsReturns(pkg.ScopeAllowed)
		step = pkg.NewPlanningStep(
			runner,
			ops,
			pkg.NewOSExecGateRunner(),
			"tok",
			scope,
			pkg.UpdateScopeBoth,
		)
		var err error
		md, err = agentlib.ParseMarkdown(ctx, planningTaskMD)
		Expect(err).To(BeNil())
	})

	It("ShouldRun is always true", func() {
		should, err := step.ShouldRun(ctx, md)
		Expect(err).To(BeNil())
		Expect(should).To(BeTrue())
	})

	Describe("installation-scope allowlist preflight", func() {
		It("parks NeedsInput before clone when the repo is outside the installation", func() {
			scope.AllowsReturns(pkg.ScopeDenied)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("bborbe/demo"))
			Expect(result.Message).To(ContainSubstring("allowlist"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})

		It(
			"proceeds on unknown verdict (PAT fallback / API error — never treat as denial)",
			func() {
				scope.AllowsReturns(pkg.ScopeUnknown)
				runner.RunReturns(nil, stderrors.New("stop here"))
				_, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(ops.CloneAtRefCallCount()).To(Equal(1))
			},
		)
	})

	Describe("missing required frontmatter", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(
				ctx,
				"---\nassignee: github-update-go-agent\nrepo: bborbe/demo\nref: 6d1f27fabcdef\n---\n\nbody\n",
			)
			Expect(err).To(BeNil())
		})

		It("escalates NeedsInput naming the field, message only", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("clone_url"))
		})

		It("does not clone", func() {
			_, _ = step.Run(ctx, md)
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})

		It("never mutates assignee/status and never writes ## Failure", func() {
			_, _ = step.Run(ctx, md)
			_, hasFailure := md.FindSection("## Failure")
			Expect(hasFailure).To(BeFalse())
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-update-go-agent"))
			_, hasPrev := md.Frontmatter["previous_assignee"]
			Expect(hasPrev).To(BeFalse())
		})
	})

	Describe("update_scope frontmatter", func() {
		BeforeEach(func() {
			var err error
			md, err = agentlib.ParseMarkdown(
				ctx,
				"---\nassignee: github-update-go-agent\nrepo: bborbe/demo\nclone_url: git@github.com:bborbe/demo.git\nref: 6d1f27fabcdef\nupdate_scope: bogus\n---\n\nbody\n",
			)
			Expect(err).To(BeNil())
		})

		It(
			"fails naming the rejected value and the accepted set for an invalid update_scope",
			func() {
				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
				Expect(result.Message).To(ContainSubstring("bogus"))
				Expect(result.Message).To(ContainSubstring("both"))
				Expect(result.Message).To(ContainSubstring("golang"))
				Expect(result.Message).To(ContainSubstring("deps"))
			},
		)

		It("does not clone for an invalid update_scope", func() {
			_, _ = step.Run(ctx, md)
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})
	})

	Describe("clone auth failure", func() {
		BeforeEach(func() {
			ops.CloneAtRefReturns(stderrors.New("git clone: returned error: 403"))
		})

		It("fails with an App-installation hint", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("git auth failure"))
			Expect(result.Message).To(ContainSubstring("bborbe/demo"))
		})
	})

	Describe("no gate target", func() {
		BeforeEach(func() {
			// CloneAtRef creates the workdir but writes no Makefile.
			ops.CloneAtRefStub = func(ctx context.Context, url, ref, workdir string) error {
				return os.MkdirAll(workdir, 0o755)
			}
		})

		It("escalates NeedsInput before any LLM call", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring("no gate target found"))
			Expect(runner.RunCallCount()).To(Equal(0))
		})
	})

	Describe("gate target failure (empty-on-error is not clean)", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefileBroken)
		})

		It("fails naming the target and exit code, never reads empty as clean", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(
				result.Message,
			).To(MatchRegexp(`gate target "check" failed \(exit [0-9-]+\) with no parseable findings`))
			Expect(result.Message).To(ContainSubstring("make: something broken"))
			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusNeedsInput))
		})
	})

	Describe("happy path", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefile)
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"go_bump": {"from": "1.26.3", "to": "1.26.5"},
				"dep_updates_expected": true,
				"gate_targets": ["precommit", "check"],
				"vulns": [
					{"id": "GO-2026-1234", "package": "golang.org/x/text", "fixed_version": "v0.39.0", "scanner": "trivy", "action": "fix", "reason": "patched"}
				]
			}`}, nil)
		})

		It("clones the token-injected HTTPS URL at the ref", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(ops.CloneAtRefCallCount()).To(Equal(1))
			_, url, ref, _ := ops.CloneAtRefArgsForCall(0)
			Expect(url).To(Equal("https://x-access-token:tok@github.com/bborbe/demo.git"))
			Expect(ref).To(Equal("6d1f27fabcdef12345678901234567890abcdef1"))
		})

		It("embeds the parsed scanner table in the prompt as the only ID source", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, prompt := runner.RunArgsForCall(0)
			Expect(prompt).To(ContainSubstring("## Scanner Findings"))
			Expect(prompt).To(ContainSubstring("GO-2026-1234 | stdlib | 1.26.6 | osv-scanner"))
		})

		It(
			"writes a round-trippable ## Plan with Go-detected gate targets and advances to execution",
			func() {
				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
				Expect(result.NextPhase).To(Equal("execution"))

				plan, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
				Expect(err).To(BeNil())
				Expect(plan.Outcome).To(Equal(pkg.PlanOutcomeReady))
				Expect(plan.GateTargets).To(Equal([]string{"check", "vulncheck"}))
				Expect(plan.GoBump.To).To(Equal("1.26.5"))
			},
		)
	})

	Describe("update_scope=golang filters dep work out of has_work", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefile)
			var err error
			md, err = agentlib.ParseMarkdown(
				ctx,
				"---\nassignee: github-update-go-agent\nrepo: bborbe/demo\nclone_url: git@github.com:bborbe/demo.git\nref: 6d1f27fabcdef\nupdate_scope: golang\n---\n\nbody\n",
			)
			Expect(err).To(BeNil())
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": true,
				"gate_targets": ["check"],
				"vulns": []
			}`}, nil)
		})

		It("resolves to done/no-update when only dep work exists (Go current)", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
		})

		It("tells the model dep work is out of scope in the prompt", func() {
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, prompt := runner.RunArgsForCall(0)
			Expect(prompt).To(ContainSubstring("## Update Scope"))
			Expect(prompt).To(ContainSubstring("Update ONLY the Go toolchain"))
		})
	})

	Describe("park path (design D4)", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefile)
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"gate_targets": ["check"],
				"vulns": [
					{"id": "GO-2026-5932", "package": "golang.org/x/crypto/openpgp", "scanner": "govulncheck", "action": "park", "reason": "no upstream fix"},
					{"id": "CVE-2026-9999", "scanner": "govulncheck", "action": "park", "reason": "major bump required"}
				]
			}`}, nil)
		})

		It(
			"parks NeedsInput carrying the verbatim scanner rows and the 3 suppression files",
			func() {
				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
				Expect(
					result.Message,
				).To(ContainSubstring("GO-2026-5932 (scanner=govulncheck, fixed_version=v0.38.0)"))
				Expect(
					result.Message,
				).To(ContainSubstring("CVE-2026-9999 (scanner=govulncheck, fixed_version=v0.36.0)"))
				Expect(result.Message).To(ContainSubstring("VULNCHECK_IGNORE"))
				Expect(result.Message).To(ContainSubstring(".osv-scanner.toml"))
				Expect(result.Message).To(ContainSubstring(".trivyignore"))
				Expect(result.Message).NotTo(ContainSubstring("no upstream fix"))
				Expect(result.Message).NotTo(ContainSubstring("major bump required"))
			},
		)

		It("still records the ## Plan for the operator", func() {
			_, _ = step.Run(ctx, md)
			plan, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(plan.Vulns).To(HaveLen(2))
		})

		It("never mutates assignee (controller owns the envelope)", func() {
			_, _ = step.Run(ctx, md)
			assignee, _ := md.Frontmatter.String("assignee")
			Expect(assignee).To(Equal("github-update-go-agent"))
		})
	})

	Describe("fabricated plan ID rejection", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefile)
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"vulns": [
					{"id": "GO-2025-3283", "package": "golang.org/x/text", "action": "fix", "reason": "patched"}
				]
			}`}, nil)
		})

		It("fails naming the fabricated ID and never parks", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("GO-2025-3283"))
			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusNeedsInput))
		})
	})

	Describe("prefix-collision plan ID rejection", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefilePrefixCollision)
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"vulns": [
					{"id": "GO-2026-50260", "package": "golang.org/x/text", "action": "fix", "reason": "patched"}
				]
			}`}, nil)
		})

		It("rejects the prefix-shared ID", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("GO-2026-50260"))
		})
	})

	Describe("no_update_needed", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefileEmpty)
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "no_update_needed",
				"has_work": false,
				"dep_updates_expected": false,
				"reason": "already on latest Go, gate clean"
			}`}, nil)
		})

		It("completes the task: Done + NextPhase done", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("done"))
		})
	})

	Describe("unparseable claude output", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefile)
			runner.RunReturns(&claudelib.ClaudeResult{Result: "sorry, no json here"}, nil)
		})

		It("fails (controller retries)", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("parse planning output"))
		})
	})

	Describe("environment-claim needs_input refutation", func() {
		var workdir string

		BeforeEach(func() {
			setupFixture(fixtureMakefile)
			workdir = filepath.Join(os.TempDir(), "github-update-go-test-task-1")
		})

		It("refutes a false workdir/sandbox claim — failed, assignee not cleared", func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "needs_input",
				"has_work": false,
				"reason": "cannot access workdir /tmp/github-update-go-test-task-1 — all filesystem access is blocked by sandbox restrictions"
			}`}, nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring(workdir))
			Expect(result.Message).To(ContainSubstring("sandbox"))
		})

		It("refutes an allowed-paths claim via the existing workdir", func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "needs_input",
				"has_work": false,
				"reason": "directory not in allowed paths (/agent only)"
			}`}, nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring(workdir))
			Expect(result.Message).To(ContainSubstring("allowed paths"))
		})

		It("keeps needs_input unchanged for a non-environment reason", func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "needs_input",
				"has_work": false,
				"reason": "no fixed version available"
			}`}, nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(Equal("no fixed version available"))
		})
	})
})
