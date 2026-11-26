// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
	libtime "github.com/bborbe/time"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

// fleetFixtureMakefile defines one gate target whose output the mocked
// GateRunner supplies.
const fleetFixtureMakefile = ".PHONY: check\ncheck:\n\t@:\n"

const fleetGoMod = "module github.com/bborbe/example\n\ngo 1.27.0\n"

const fleetConsentYAML = "goUpdate:\n  autoUpdate: true\n"

var _ = Describe("FleetScanStep", func() {
	var (
		ctx        context.Context
		lister     *mocks.RepoLister
		files      *mocks.RepoFileFetcher
		consent    *mocks.MaintainerConfigFetcher
		ops        *mocks.GitOps
		gate       *mocks.GateRunner
		classifier *mocks.IgnoreClassifier
		fixedNow   libtime.DateTime
	)

	BeforeEach(func() {
		ctx = context.Background()
		lister = &mocks.RepoLister{}
		files = &mocks.RepoFileFetcher{}
		consent = &mocks.MaintainerConfigFetcher{}
		ops = &mocks.GitOps{}
		gate = &mocks.GateRunner{}
		classifier = &mocks.IgnoreClassifier{}
		fixedNow = libtime.DateTime(time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC))

		files.FetchFileStub = func(context.Context, string, string) ([]byte, error) {
			return []byte(fleetGoMod), nil
		}
		consent.FetchStub = func(context.Context, string, string, string) ([]byte, error) {
			return []byte(fleetConsentYAML), nil
		}
		ops.ResolveDefaultBranchStub = func(_ context.Context, url string) (string, string, error) {
			return "main", "sha-" + url, nil
		}
		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(workdir, "Makefile"),
				[]byte(fleetFixtureMakefile),
				0o644,
			)
		}
		gate.RunTargetFullStub = func(context.Context, string, string) (string, int, error) {
			return "", 0, nil
		}
	})

	newStepWithStore := func(
		owner string,
		allowlist []string,
		store pkg.ScanTaskStore,
	) agentlib.Step {
		return pkg.NewFleetScanStep(pkg.FleetScanDeps{
			Config: pkg.FleetScanConfig{
				Owner:     owner,
				Allowlist: allowlist,
				Stage:     "prod",
			},
			GhToken:    "token",
			Lister:     lister,
			Files:      files,
			Consent:    consent,
			GitOps:     ops,
			Gate:       gate,
			Classifier: classifier,
			CurrentDateTime: libtime.CurrentDateTimeGetterFunc(
				func() libtime.DateTime { return fixedNow },
			),
			Store: store,
		})
	}

	newStep := func(owner string, allowlist []string) agentlib.Step {
		return newStepWithStore(owner, allowlist, nil)
	}

	newMD := func() *agentlib.Markdown {
		md, err := agentlib.ParseMarkdown(
			ctx,
			"---\ntask_type: github-fleet-scan\n---\n\nNightly scan\n",
		)
		Expect(err).To(BeNil())
		return md
	}

	It("scans two consenting repos, records findings, and removes suppressed IDs", func() {
		const repoA = "bborbe/scan-happy-a"
		const repoB = "bborbe/scan-happy-b"
		lister.ListReposReturns([]string{repoA, repoB}, nil)

		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(
				filepath.Join(workdir, "Makefile"),
				[]byte(fleetFixtureMakefile),
				0o644,
			); err != nil {
				return err
			}
			if strings.Contains(workdir, "scan-happy-a") {
				return os.WriteFile(
					filepath.Join(workdir, ".trivyignore"),
					[]byte("GO-2026-9999\n"),
					0o644,
				)
			}
			return nil
		}
		gate.RunTargetFullStub = func(_ context.Context, workdir, _ string) (string, int, error) {
			if strings.Contains(workdir, "scan-happy-a") {
				return "GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6\n" +
					"GO-2026-9999 | stdlib | 1.26.5 | fixed 1.26.6\n", 0, nil
			}
			return "", 0, nil
		}

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(result.NextPhase).To(BeEmpty())

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Scanned).To(Equal(2))
		Expect(report.ScannedAt).To(Equal(fixedNow.String()))

		byRepo := map[string]pkg.ScanRepoResult{}
		for _, repo := range report.Repos {
			byRepo[repo.Repo] = repo
		}
		Expect(byRepo[repoA].VulnIDs).To(Equal([]string{"GO-2026-1234"}))
		Expect(byRepo[repoA].CloneURL).To(Equal("git@github.com:" + repoA + ".git"))
		Expect(byRepo[repoB].VulnIDs).To(BeEmpty())
	})

	It("returns NeedsInput naming OWNER when the owner is empty", func() {
		result, err := newStep("", nil).Run(ctx, newMD())
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(result.Message).To(ContainSubstring("OWNER"))
	})

	It("completes with scanned=0 and writes the section for an empty population", func() {
		lister.ListReposReturns(nil, nil)
		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Scanned).To(Equal(0))
	})

	It("fails when the lister fails", func() {
		lister.ListReposReturns(nil, stderrors.New("boom"))
		result, err := newStep("bborbe", nil).Run(ctx, newMD())
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
	})

	It("fails on a rate limit while gating, naming the repo and scanned=0", func() {
		const repoA = "bborbe/scan-rate-a"
		const repoB = "bborbe/scan-rate-b"
		lister.ListReposReturns([]string{repoA, repoB}, nil)
		files.FetchFileStub = func(_ context.Context, repo, _ string) ([]byte, error) {
			if repo == repoB {
				return nil, pkg.ErrFleetRateLimited
			}
			return []byte(fleetGoMod), nil
		}

		result, err := newStep("bborbe", nil).Run(ctx, newMD())
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(result.Message).To(ContainSubstring(repoB))
		Expect(result.Message).To(ContainSubstring("scanned=0"))
	})

	It("skips a repo whose clone fails and still scans the other", func() {
		const repoA = "bborbe/scan-clone-a"
		const repoB = "bborbe/scan-clone-b"
		lister.ListReposReturns([]string{repoA, repoB}, nil)
		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if strings.Contains(workdir, "scan-clone-a") {
				return stderrors.New("clone failed")
			}
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(workdir, "Makefile"),
				[]byte(fleetFixtureMakefile),
				0o644,
			)
		}

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Scanned).To(Equal(1))
	})

	It("keeps findings from a gate target that exits non-zero with output", func() {
		const repo = "bborbe/scan-gate-error"
		lister.ListReposReturns([]string{repo}, nil)
		gate.RunTargetFullStub = func(context.Context, string, string) (string, int, error) {
			return "GO-2026-4242 | stdlib | 1.26.5 | fixed 1.26.6\n",
				1, stderrors.New("gate failed")
		}

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Scanned).To(Equal(1))
		Expect(report.Repos[0].VulnIDs).To(Equal([]string{"GO-2026-4242"}))
	})

	It("classifies a stale-population repo and keeps only the withdrawn verdict", func() {
		const repo = "bborbe/scan-stale-withdrawn"
		const plainRepo = "bborbe/scan-stale-plain"
		lister.ListReposReturns([]string{repo, plainRepo}, nil)
		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(
				filepath.Join(workdir, "Makefile"),
				[]byte(fleetFixtureMakefile),
				0o644,
			); err != nil {
				return err
			}
			if err := os.WriteFile(
				filepath.Join(workdir, "go.mod"),
				[]byte(fleetGoMod),
				0o644,
			); err != nil {
				return err
			}
			if strings.Contains(workdir, "scan-stale-withdrawn") {
				return os.WriteFile(
					filepath.Join(workdir, ".trivyignore"),
					[]byte("GO-2026-0001\nGO-2026-0002\n"),
					0o644,
				)
			}
			return nil
		}
		classifier.ClassifyStub = func(
			_ context.Context, _ string, ids []string,
		) []pkg.IgnoreClassification {
			out := make([]pkg.IgnoreClassification, 0, len(ids))
			for _, id := range ids {
				out = append(out, pkg.IgnoreClassification{
					ID:      id,
					Outcome: pkg.IgnoreOutcomeLegitimate,
					Reason:  pkg.IgnoreReasonVersionAffected,
				})
			}
			out[0] = pkg.IgnoreClassification{
				ID:      out[0].ID,
				Outcome: pkg.IgnoreOutcomeStale,
				Reason:  pkg.IgnoreReasonWithdrawn,
			}
			return out
		}

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		byRepo := map[string]pkg.ScanRepoResult{}
		for _, repo := range report.Repos {
			byRepo[repo.Repo] = repo
		}
		Expect(byRepo[repo].StaleIDs).To(Equal([]string{"GO-2026-0001"}))
		Expect(byRepo[plainRepo].StaleIDs).To(BeNil())
		Expect(classifier.ClassifyCallCount()).To(Equal(1))

		raw, err := md.Marshal(ctx)
		Expect(err).To(BeNil())
		Expect(raw).To(ContainSubstring(`"stale_ids"`))
		// The repo with no stale findings omits the key entirely (omitempty).
		Expect(strings.Count(raw, "stale_ids")).To(Equal(1))
	})

	It("does not classify a repo outside the stale population", func() {
		const repo = "bborbe/scan-unparsable"
		lister.ListReposReturns([]string{repo}, nil)
		files.FetchFileStub = func(context.Context, string, string) ([]byte, error) {
			return []byte("this is not a go.mod {{{"), nil
		}
		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(
				filepath.Join(workdir, "Makefile"),
				[]byte(fleetFixtureMakefile),
				0o644,
			); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(workdir, ".trivyignore"),
				[]byte("GO-2026-0001\n"),
				0o644,
			)
		}

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Repos).To(HaveLen(1))
		Expect(report.Repos[0].StaleIDs).To(BeNil())
		Expect(classifier.ClassifyCallCount()).To(Equal(0))
	})

	It("does not classify a stale-population repo with no suppressions", func() {
		const repo = "bborbe/scan-no-suppressions"
		lister.ListReposReturns([]string{repo}, nil)
		ops.CloneAtRefStub = func(_ context.Context, _, _, workdir string) error {
			if err := os.MkdirAll(workdir, 0o755); err != nil {
				return err
			}
			return os.WriteFile(
				filepath.Join(workdir, "Makefile"),
				[]byte(fleetFixtureMakefile),
				0o644,
			)
		}

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Repos).To(HaveLen(1))
		Expect(report.Repos[0].StaleIDs).To(BeNil())
		Expect(classifier.ClassifyCallCount()).To(Equal(0))
	})

	It("returns an error when the context is cancelled before the scan loop", func() {
		const repo = "bborbe/scan-cancelled"
		lister.ListReposReturns([]string{repo}, nil)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()

		_, err := newStep("bborbe", nil).Run(cancelled, newMD())
		Expect(err).NotTo(BeNil())
	})

	// seedVulnRepo configures the fakes so every listed repo yields one vuln
	// finding named by its own suffix.
	seedVulnRepos := func(repos ...string) {
		lister.ListReposReturns(repos, nil)
		gate.RunTargetFullStub = func(_ context.Context, workdir, _ string) (string, int, error) {
			for i, repo := range repos {
				if strings.Contains(workdir, strings.TrimPrefix(repo, "bborbe/")) {
					return fmt.Sprintf(
						"GO-2026-%d | stdlib | 1.26.5 | fixed 1.26.6\n",
						1000+i,
					), 0, nil
				}
			}
			return "", 0, nil
		}
	}

	It("files nothing on a second run over an unchanged fleet", func() {
		const repoA = "bborbe/scan-pub-a"
		const repoB = "bborbe/scan-pub-b"
		seedVulnRepos(repoA, repoB)

		seen := map[agentlib.TaskIdentifier]bool{}
		store := &mocks.ScanTaskStore{}
		store.HasStub = func(_ context.Context, id agentlib.TaskIdentifier) (bool, error) {
			return seen[id], nil
		}
		store.PublishStub = func(_ context.Context, cmd task.CreateCommand) error {
			seen[cmd.TaskIdentifier] = true
			return nil
		}
		step := newStepWithStore("bborbe", nil, store)

		firstMD := newMD()
		first, err := step.Run(ctx, firstMD)
		Expect(err).To(BeNil())
		Expect(first.Status).To(Equal(agentlib.AgentStatusDone))
		firstReport, err := agentlib.ExtractSection[pkg.FleetScanReport](
			ctx, firstMD, "## Fleet Scan",
		)
		Expect(err).To(BeNil())
		Expect(firstReport.Scanned).To(Equal(2))
		Expect(firstReport.Filed).To(Equal(2))
		Expect(firstReport.AlreadyRecorded).To(Equal(0))
		Expect(store.PublishCallCount()).To(Equal(2))

		secondMD := newMD()
		second, err := step.Run(ctx, secondMD)
		Expect(err).To(BeNil())
		Expect(second.Status).To(Equal(agentlib.AgentStatusDone))
		secondReport, err := agentlib.ExtractSection[pkg.FleetScanReport](
			ctx, secondMD, "## Fleet Scan",
		)
		Expect(err).To(BeNil())
		Expect(secondReport.Scanned).To(Equal(2))
		Expect(secondReport.Filed).To(Equal(0))
		Expect(secondReport.AlreadyRecorded).To(Equal(2))
		Expect(store.PublishCallCount()).To(Equal(2))
	})

	It("fails naming the filed count when the task lookup fails", func() {
		const repo = "bborbe/scan-pub-lookup"
		seedVulnRepos(repo)
		store := &mocks.ScanTaskStore{}
		store.HasReturns(false, stderrors.New("lookup boom"))

		result, err := newStepWithStore("bborbe", nil, store).Run(ctx, newMD())
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(result.Message).To(ContainSubstring("filed=0"))
		Expect(store.PublishCallCount()).To(Equal(0))
	})

	It("fails with the filed count when a publish fails mid-run", func() {
		seedVulnRepos("bborbe/scan-pub-a", "bborbe/scan-pub-b", "bborbe/scan-pub-c")
		calls := 0
		store := &mocks.ScanTaskStore{}
		store.PublishStub = func(_ context.Context, _ task.CreateCommand) error {
			calls++
			if calls == 2 {
				return stderrors.New("publish boom")
			}
			return nil
		}

		result, err := newStepWithStore("bborbe", nil, store).Run(ctx, newMD())
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(result.Message).To(ContainSubstring("filed=1"))
		Expect(store.PublishCallCount()).To(Equal(2))
	})

	It("completes with filed=0 when no store is configured", func() {
		const repo = "bborbe/scan-pub-nostore"
		seedVulnRepos(repo)

		md := newMD()
		result, err := newStep("bborbe", nil).Run(ctx, md)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))

		report, err := agentlib.ExtractSection[pkg.FleetScanReport](ctx, md, "## Fleet Scan")
		Expect(err).To(BeNil())
		Expect(report.Filed).To(Equal(0))
		Expect(report.AlreadyRecorded).To(Equal(0))
	})
})
