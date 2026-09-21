// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"strings"

	agentlib "github.com/bborbe/agent"
	claudelib "github.com/bborbe/agent/claude"
	domain "github.com/bborbe/vault-cli/pkg/domain"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
	"github.com/bborbe/github-update-go-agent/pkg/maintainerconfig"
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

// fixtureMakefileTimedOut defines a gate target that fails by hanging — its
// output carries Go's test-timeout panic text and no advisory IDs, so it
// exercises the timeout-classified variant of the empty-on-error escalation.
var fixtureMakefileTimedOut = ".PHONY: check\n" +
	"check:\n" +
	"\t@echo 'panic: test timed out after 10m0s' >&2; exit 1\n"

// fixtureMakefileBrokenWithFindings defines a gate target that exits non-zero
// while still emitting a parseable advisory row — only the zero-rows-on-error
// case parks (spec 006 Desired Behavior 3: rows still join the scanner table).
var fixtureMakefileBrokenWithFindings = ".PHONY: check\n" +
	"check:\n" +
	"\t@echo 'GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6'; exit 1\n"

var _ = Describe("PlanningStep", func() {
	var (
		ctx              context.Context
		runner           *mocks.ClaudeRunnerMock
		ops              *mocks.GitOps
		scope            *mocks.InstallationScope
		maintainerConfig *mocks.MaintainerConfigFetcher
		step             agentlib.Step
		md               *agentlib.Markdown
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

	// setupFixtureWithWorkflow mirrors a clone that also carries a
	// `.github/workflows/ci.yml` with the given content — used to exercise the
	// CI-pin preflight without a real git clone.
	setupFixtureWithWorkflow := func(workflowContent string) {
		ops.CloneAtRefStub = func(ctx context.Context, url, ref, workdir string) error {
			if err := os.MkdirAll(filepath.Join(workdir, ".github", "workflows"), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(workdir, "Makefile"), []byte(fixtureMakefile), 0o644); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(workdir, ".github", "workflows", "ci.yml"),
				[]byte(workflowContent), 0o644)
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		runner = &mocks.ClaudeRunnerMock{}
		ops = &mocks.GitOps{}
		scope = &mocks.InstallationScope{}
		maintainerConfig = &mocks.MaintainerConfigFetcher{}
		scope.AllowsReturns(pkg.ScopeAllowed)
		ops.ResolveDefaultBranchHeadReturns("0cafebabe1234567890abcdef1234567890abcdef", nil)
		// Default consent: repo opted in, so the gate passes and the test
		// exercises the downstream inspection path. Gate-specific specs
		// override this return.
		maintainerConfig.FetchReturns([]byte("goUpdate:\n  autoUpdate: true\n"), nil)
		step = pkg.NewPlanningStep(
			runner,
			ops,
			pkg.NewOSExecGateRunner(),
			"tok",
			scope,
			maintainerConfig,
			pkg.UpdateScopeBoth,
		)
		var err error
		md, err = agentlib.ParseMarkdown(ctx, uniqueTaskMD(planningTaskMD))
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

	Describe(".maintainer.yaml consent gate (goUpdate.autoUpdate)", func() {
		It("proceeds when goUpdate.autoUpdate=true (no fetch call surfaced on plan)", func() {
			maintainerConfig.FetchReturns([]byte("goUpdate:\n  autoUpdate: true\n"), nil)
			runner.RunReturns(nil, stderrors.New("stop here"))
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(ops.CloneAtRefCallCount()).To(Equal(1))
		})

		It("skips with auto_update_disabled when goUpdate.autoUpdate=false", func() {
			maintainerConfig.FetchReturns([]byte("goUpdate:\n  autoUpdate: false\n"), nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal(domain.TaskPhaseDone.String()))
			Expect(result.Message).To(ContainSubstring("auto_update_disabled"))
			// No clone — the gate short-circuits before any update work.
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})

		It("skips with auto_update_disabled when .maintainer.yaml is absent (404)", func() {
			maintainerConfig.FetchReturns(nil, maintainerconfig.ErrFileNotFound)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.Message).To(ContainSubstring("auto_update_disabled"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})

		It("skips with auto_update_disabled + ConfigFetchWarning on transport error", func() {
			maintainerConfig.FetchReturns(nil, stderrors.New("http 502"))
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.Message).To(ContainSubstring("auto_update_disabled"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
			// The ## Plan block carries the warning so the skip is
			// distinguishable from a deliberate false.
			section, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(section.ConfigFetchWarning).NotTo(BeEmpty())
			Expect(section.Outcome).To(Equal(pkg.PlanOutcomeNoUpdateNeeded))
		})

		It("fails closed to human_review on malformed YAML (invalid_config)", func() {
			maintainerConfig.FetchReturns([]byte("goUpdate: [unclosed"), nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.NextPhase).To(Equal(domain.TaskPhaseHumanReview.String()))
			Expect(result.Message).To(HavePrefix("planning: "))
			Expect(result.Message).To(ContainSubstring("invalid .maintainer.yaml"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
			section, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(section.Outcome).To(Equal(pkg.PlanOutcomeFailed))
			Expect(section.ErrorCategory).To(Equal(pkg.ErrorCategoryInvalidConfig))
			Expect(section.InvalidField).To(Equal("goUpdate.autoUpdate"))
		})

		It("fails closed to human_review on non-boolean goUpdate.autoUpdate", func() {
			// `yes` is a valid YAML 1.1 boolean (yaml.v3 resolves it to true);
			// use a genuine non-boolean scalar so ParseStrict errors.
			maintainerConfig.FetchReturns([]byte("goUpdate:\n  autoUpdate: sometimes\n"), nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.NextPhase).To(Equal(domain.TaskPhaseHumanReview.String()))
			Expect(result.Message).To(HavePrefix("planning: "))
			Expect(result.Message).To(ContainSubstring("invalid .maintainer.yaml"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
		})
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
			Expect(ops.ResolveDefaultBranchHeadCallCount()).To(Equal(0))
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

	Describe("CI-pin preflight (hardcoded go-version in workflow)", func() {
		BeforeEach(func() {
			setupFixtureWithWorkflow(`name: CI
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.26.5'
          cache: true
`)
		})

		It("escalates NeedsInput with the workflow file + manual fix, before any LLM call", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(result.Message).To(ContainSubstring(".github/workflows/ci.yml"))
			Expect(result.Message).To(ContainSubstring("go-version-file: go.mod"))
			Expect(result.Message).To(ContainSubstring("cannot edit workflows"))
			Expect(runner.RunCallCount()).To(Equal(0))
		})
	})

	Describe("CI-pin preflight (matrix go-version — not a hardcode)", func() {
		BeforeEach(func() {
			setupFixtureWithWorkflow(`name: Test
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    strategy:
      matrix:
        go-version: ['1.25.11', '1.26.5']
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: ${{ matrix.go-version }}
`)
		})

		It("proceeds past the preflight to inspection (no escalation)", func() {
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "no_update_needed",
				"has_work": false,
				"gate_targets": ["precommit"]
			}`}, nil)
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(runner.RunCallCount()).To(Equal(1))
		})
	})

	Describe("gate target failure (empty-on-error parks NeedsInput, never retried)", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefileBroken)
		})

		It("parks NeedsInput naming target, exit code, and repair action", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(
				result.Message,
			).To(MatchRegexp(`gate target "check" failed \(exit [0-9-]+\) with no parseable findings`))
			Expect(result.Message).To(ContainSubstring("make: something broken"))
			Expect(result.Message).To(ContainSubstring("a re-run reproduces the identical result"))
			Expect(result.Message).To(ContainSubstring("Fix the target"))
			Expect(result.Message).To(ContainSubstring("next HEAD"))
			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusFailed))
		})
	})

	Describe("gate target timeout (empty-on-error parks NeedsInput, names the timeout)", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefileTimedOut)
		})

		It("parks NeedsInput naming the timeout, exit code, and repair action", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
			Expect(
				result.Message,
			).To(MatchRegexp(`gate target "check" failed \(exit [0-9-]+\) — test timed out with no parseable findings`))
			Expect(result.Message).To(ContainSubstring("panic: test timed out after 10m0s"))
			Expect(result.Message).To(ContainSubstring("a re-run reproduces the identical result"))
			Expect(result.Message).To(ContainSubstring("Fix the target"))
			Expect(result.Message).To(ContainSubstring("next HEAD"))
			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusFailed))
		})
	})

	Describe("failing gate that emits parseable findings contributes rows", func() {
		BeforeEach(func() {
			setupFixture(fixtureMakefileBrokenWithFindings)
			runner.RunReturns(nil, stderrors.New("stop here"))
		})

		It("proceeds past the empty-on-error branch and reaches the inspection call", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(runner.RunCallCount()).To(Equal(1))
			// Failed comes from the stub runner error, not the gate branch.
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		})
	})

	Describe("current-HEAD resolution failure", func() {
		BeforeEach(func() {
			ops.ResolveDefaultBranchHeadReturns(
				"",
				stderrors.New("git ls-remote --symref HEAD: Repository not found"),
			)
		})

		It("fails naming the resolution step and never falls back to the stale pinned ref", func() {
			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("resolve current default-branch HEAD"))
			Expect(result.Message).To(ContainSubstring("Repository not found"))
			Expect(ops.CloneAtRefCallCount()).To(Equal(0))
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

		It(
			"clones the token-injected HTTPS URL at the resolved current HEAD, not the stale pinned ref",
			func() {
				_, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(ops.CloneAtRefCallCount()).To(Equal(1))
				_, url, ref, _ := ops.CloneAtRefArgsForCall(0)
				Expect(url).To(Equal("https://x-access-token:tok@github.com/bborbe/demo.git"))
				Expect(ref).To(Equal("0cafebabe1234567890abcdef1234567890abcdef"))
				Expect(ops.ResolveDefaultBranchHeadCallCount()).To(Equal(1))
			},
		)

		It(
			"keeps the pinned ref recorded for provenance — frontmatter unchanged after the run",
			func() {
				_, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				ref, _ := md.Frontmatter.String("ref")
				Expect(ref).To(Equal("6d1f27fabcdef12345678901234567890abcdef1"))
			},
		)

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
			// Derived from the spec's own task_identifier (see uniqueTaskMD):
			// the workdir is per-spec now, so a hardcoded path would be stale.
			id, _ := md.Frontmatter.String("task_identifier")
			workdir = filepath.Join(os.TempDir(), "github-update-go-"+id)
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

	Describe("external advisory ingestion", func() {
		// advisoryTaskMD renders the planning fixture task with an `advisory`
		// block spliced into its frontmatter.
		advisoryTaskMD := func(advisoryBlock string) string {
			return uniqueTaskMD("---\n" +
				"task_type: github-update-go\n" +
				"assignee: github-update-go-agent\n" +
				"phase: planning\n" +
				"status: in_progress\n" +
				"repo: bborbe/demo\n" +
				"clone_url: git@github.com:bborbe/demo.git\n" +
				"ref: 6d1f27fabcdef12345678901234567890abcdef1\n" +
				"task_identifier: test-task-1\n" +
				advisoryBlock +
				"---\n\nUpdate Go bborbe/demo\n")
		}

		// advisoryBlockFor renders the frozen four-key block.
		advisoryBlockFor := func(id, pkg, fixedVersion, source string) string {
			return "advisory:\n" +
				"  id: " + id + "\n" +
				"  package: " + pkg + "\n" +
				"  fixed_version: " + fixedVersion + "\n" +
				"  source: " + source + "\n"
		}

		// advisoryCVE7001 is the collision ID used by both AC5 fixtures.
		const advisoryCVE7001 = "CVE-2026-7001"

		// scannerSection slices the `## Scanner Findings` section out of the
		// captured prompt. The delimiter must be the appended section's exact
		// shape: the planning prompt module itself contains the backticked
		// text `## Scanner Findings`, so a bare Index would find the module's
		// own sentence instead of the table. The `## Task` tail is also
		// required — it embeds the frontmatter, which carries the advisory ID
		// too, so a whole-prompt count would see 2.
		scannerSection := func(prompt string) string {
			const delimiter = "\n\n## Scanner Findings\n\n"
			start := strings.LastIndex(prompt, delimiter)
			Expect(start).NotTo(Equal(-1))
			rest := prompt[start+len(delimiter):]
			end := strings.Index(rest, "\n\n## Task\n\n")
			Expect(end).NotTo(Equal(-1))
			return rest[:end]
		}

		// onlyLineContaining asserts exactly one line of the section carries
		// the ID and returns it.
		onlyLineContaining := func(section, id string) string {
			var matches []string
			for _, line := range strings.Split(section, "\n") {
				if strings.Contains(line, id) {
					matches = append(matches, line)
				}
			}
			Expect(matches).To(HaveLen(1))
			return matches[0]
		}

		var markerPath string

		BeforeEach(func() {
			// Derived from the spec's own task_identifier (see uniqueTaskMD):
			// the workdir is per-spec now, so a hardcoded path would both be
			// stale and re-introduce the cross-spec marker collision this
			// block's absent-marker assertion depends on.
			id, _ := md.Frontmatter.String("task_identifier")
			markerPath = filepath.Join(
				os.TempDir(),
				"github-update-go-"+id,
				"gate-ran-marker",
			)
		})

		// fixtureMakefileAdvisoryClean's every gate target touches the marker
		// file and emits no advisory IDs — so a surviving marker proves a gate
		// target ran, and an absent one proves none did.
		const fixtureMakefileAdvisoryClean = ".PHONY: check vulncheck\n" +
			"check:\n" +
			"\t@touch gate-ran-marker\n" +
			"vulncheck:\n" +
			"\t@touch gate-ran-marker\n"

		// fixtureMakefileAdvisoryCollisionEmpty's check target emits an
		// ID-bearing line in no recognized scanner shape, so the parsed row
		// carries the ID and an EMPTY fixed version.
		const fixtureMakefileAdvisoryCollisionEmpty = ".PHONY: check vulncheck\n" +
			"check:\n" +
			"\t@touch gate-ran-marker\n" +
			"\t@echo '" + advisoryCVE7001 + " affected in golang.org/x/net'\n" +
			"vulncheck:\n" +
			"\t@touch gate-ran-marker\n"

		// fixtureMakefileAdvisoryCollisionFixed's check target emits an
		// osv-shaped line, so the parsed row carries a NON-EMPTY fixed version.
		const fixtureMakefileAdvisoryCollisionFixed = ".PHONY: check vulncheck\n" +
			"check:\n" +
			"\t@touch gate-ran-marker\n" +
			"\t@echo '" + advisoryCVE7001 + " | golang.org/x/net | 1.26.5 | fixed v0.40.0'\n" +
			"vulncheck:\n" +
			"\t@touch gate-ran-marker\n"

		It("admits a valid advisory row and drives a fix for it", func() {
			setupFixture(fixtureMakefileAdvisoryClean)
			md, err := agentlib.ParseMarkdown(ctx, advisoryTaskMD(advisoryBlockFor(
				"CVE-2026-4242", "golang.org/x/text", "v0.39.0", "osv-feed",
			)))
			Expect(err).To(BeNil())
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"vulns": [
					{"id": "CVE-2026-4242", "package": "golang.org/x/text", "fixed_version": "v0.39.0", "scanner": "external:osv-feed", "action": "fix", "reason": "advisory fix"}
				]
			}`}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(runner.RunCallCount()).To(Equal(1))

			_, prompt := runner.RunArgsForCall(0)
			Expect(
				prompt,
			).To(ContainSubstring("CVE-2026-4242 | golang.org/x/text | v0.39.0 | external:osv-feed"))

			plan, err := agentlib.ExtractSection[pkg.PlanOutput](ctx, md, "## Plan")
			Expect(err).To(BeNil())
			Expect(plan.Vulns).To(HaveLen(1))
			Expect(plan.Vulns[0].ID).To(Equal("CVE-2026-4242"))
			Expect(plan.Vulns[0].Action).To(Equal(pkg.VulnActionFix))
			Expect(plan.Vulns[0].FixedVersion).To(Equal("v0.39.0"))
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
			Expect(result.NextPhase).To(Equal("execution"))
		})

		It("still rejects an ID present in neither provenance", func() {
			setupFixture(fixtureMakefileAdvisoryClean)
			md, err := agentlib.ParseMarkdown(ctx, advisoryTaskMD(advisoryBlockFor(
				"CVE-2026-4242", "golang.org/x/text", "v0.39.0", "osv-feed",
			)))
			Expect(err).To(BeNil())
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"vulns": [
					{"id": "CVE-2026-4242", "package": "golang.org/x/text", "fixed_version": "v0.39.0", "scanner": "external:osv-feed", "action": "fix", "reason": "advisory fix"},
					{"id": "GO-2025-3283", "package": "golang.org/x/net", "action": "fix", "reason": "fabricated"}
				]
			}`}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
			Expect(result.Message).To(ContainSubstring("GO-2025-3283"))
			Expect(result.Status).NotTo(Equal(agentlib.AgentStatusNeedsInput))
			_, found := md.FindSection("## Plan")
			Expect(found).To(BeFalse())
		})

		DescribeTable("rejects a malformed block loudly, before any gate target runs",
			func(block, field, valueSubstring string) {
				setupFixture(fixtureMakefileAdvisoryClean)
				md, err := agentlib.ParseMarkdown(ctx, advisoryTaskMD(block))
				Expect(err).To(BeNil())

				result, err := step.Run(ctx, md)
				Expect(err).To(BeNil())
				Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
				Expect(result.Message).To(ContainSubstring("field=" + field))
				Expect(result.Message).To(ContainSubstring(valueSubstring))
				Expect(runner.RunCallCount()).To(Equal(0))

				_, statErr := os.Stat(markerPath)
				Expect(os.IsNotExist(statErr)).To(BeTrue())
			},
			Entry("ID outside the accepted shapes",
				advisoryBlockFor("FOO-2026-1", "golang.org/x/text", "v0.39.0", "osv-feed"),
				"id", "FOO-2026-1"),
			Entry("empty package",
				advisoryBlockFor("CVE-2026-4242", `""`, "v0.39.0", "osv-feed"),
				"package", "value="),
			Entry("unparseable fixed_version",
				advisoryBlockFor("CVE-2026-4242", "golang.org/x/text", "not-a-version", "osv-feed"),
				"fixed_version", "not-a-version"),
			Entry(
				"missing key",
				"advisory:\n  id: CVE-2026-4242\n  package: golang.org/x/text\n  fixed_version: v0.39.0\n",
				"source",
				"value=<nil>",
			),
			Entry("list instead of mapping",
				"advisory:\n  - id: CVE-2026-4242\n    package: golang.org/x/text\n",
				"advisory", "CVE-2026-4242"),
		)

		It("keeps the advisory fixed version when the scanner row carries none", func() {
			setupFixture(fixtureMakefileAdvisoryCollisionEmpty)
			md, err := agentlib.ParseMarkdown(ctx, advisoryTaskMD(advisoryBlockFor(
				advisoryCVE7001, "golang.org/x/net", "v0.36.0", "osv-feed",
			)))
			Expect(err).To(BeNil())
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"vulns": [
					{"id": "CVE-2026-7001", "package": "golang.org/x/net", "fixed_version": "v0.36.0", "scanner": "external:osv-feed", "action": "fix", "reason": "advisory fix"}
				]
			}`}, nil)

			result, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

			_, prompt := runner.RunArgsForCall(0)
			Expect(
				onlyLineContaining(scannerSection(prompt), advisoryCVE7001),
			).To(Equal("CVE-2026-7001 | golang.org/x/net | v0.36.0 | external:osv-feed"))
		})

		It("keeps the scanner row and its label when it carries a real fixed version", func() {
			setupFixture(fixtureMakefileAdvisoryCollisionFixed)
			runner.RunReturns(&claudelib.ClaudeResult{Result: `{
				"outcome": "ready",
				"has_work": true,
				"dep_updates_expected": false,
				"vulns": [
					{"id": "CVE-2026-7001", "package": "golang.org/x/net", "fixed_version": "v0.40.0", "scanner": "osv-scanner", "action": "fix", "reason": "patched"}
				]
			}`}, nil)

			// Baseline — no advisory block, the gate output alone.
			_, err := step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, baselinePrompt := runner.RunArgsForCall(0)
			baselineLine := onlyLineContaining(scannerSection(baselinePrompt), advisoryCVE7001)
			Expect(baselineLine).To(ContainSubstring("osv-scanner"))

			// Same fixture, now colliding with an advisory on the same ID.
			md, err = agentlib.ParseMarkdown(ctx, advisoryTaskMD(advisoryBlockFor(
				advisoryCVE7001, "golang.org/x/net", "v0.36.0", "osv-feed",
			)))
			Expect(err).To(BeNil())
			_, err = step.Run(ctx, md)
			Expect(err).To(BeNil())
			_, prompt := runner.RunArgsForCall(1)
			Expect(
				onlyLineContaining(scannerSection(prompt), advisoryCVE7001),
			).To(Equal(baselineLine))
			Expect(scannerSection(prompt)).NotTo(ContainSubstring("external:osv-feed"))
		})
	})
})
