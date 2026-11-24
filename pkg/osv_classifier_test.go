// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	stderrors "errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/mocks"
	"github.com/bborbe/github-update-go-agent/pkg"
)

// osvWorkdir writes content as go.mod in a fresh temp dir and returns the dir.
func osvWorkdir(content string) string {
	dir, err := os.MkdirTemp("", "osv-classifier-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })
	Expect(os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o600)).To(Succeed())
	return dir
}

// osvFetcher builds a fake whose FetchVuln serves records by ID and answers
// ErrAdvisoryNotFound for every other ID.
func osvFetcher(
	records map[string]*pkg.OSVVulnerability,
	index *pkg.GoVulnIndex,
) *mocks.OSVFetcher {
	fake := &mocks.OSVFetcher{}
	fake.FetchVulnStub = func(_ context.Context, id string) (*pkg.OSVVulnerability, error) {
		if record, ok := records[id]; ok {
			return record, nil
		}
		return nil, pkg.ErrAdvisoryNotFound
	}
	fake.FetchGoIndexReturns(index, nil)
	return fake
}

func osvAffectedGo(name string, ranges ...pkg.OSVRange) pkg.OSVAffected {
	return pkg.OSVAffected{
		Package: pkg.OSVPackage{Ecosystem: "Go", Name: name},
		Ranges:  ranges,
	}
}

func osvSemverRange(events ...pkg.OSVEvent) pkg.OSVRange {
	return pkg.OSVRange{Type: "SEMVER", Events: events}
}

func osvIntroduced(version string) pkg.OSVEvent {
	return pkg.OSVEvent{Introduced: version}
}

func osvFixed(version string) pkg.OSVEvent {
	return pkg.OSVEvent{Fixed: version}
}

// osvExpectVerdict classifies ids in workdir and asserts the single verdict.
func osvExpectVerdict(
	classifier pkg.IgnoreClassifier,
	ctx context.Context,
	workdir, id, outcome, reason string,
) pkg.IgnoreClassification {
	results := classifier.Classify(ctx, workdir, []string{id})
	Expect(results).To(HaveLen(1))
	Expect(results[0].ID).To(Equal(id))
	Expect(results[0].Outcome).To(Equal(outcome))
	Expect(results[0].Reason).To(Equal(reason))
	return results[0]
}

var _ = Describe("OSV ignore classifier", func() {
	ctx := context.Background()

	It("classifies a withdrawn record as stale/withdrawn", func() {
		workdir := osvWorkdir("module example.com/test\n\ngo 1.24.0\n")
		record := &pkg.OSVVulnerability{ID: "GO-2026-0001", Withdrawn: "2026-01-01T00:00:00Z"}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-0001": record}, &pkg.GoVulnIndex{},
		))

		osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0001", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonWithdrawn,
		)
	})

	It("resolves a merged alias through the Go index", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.0.0\n",
		)
		record := &pkg.OSVVulnerability{
			ID:        "GO-2026-4923",
			Aliases:   []string{"CVE-2026-33817"},
			Withdrawn: "2026-01-01T00:00:00Z",
		}
		index := &pkg.GoVulnIndex{
			{ID: "GO-2026-4923", Aliases: []string{"CVE-2026-33817"}},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-4923": record}, index,
		))

		osvExpectVerdict(
			classifier, ctx, workdir,
			"CVE-2026-33817", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonWithdrawn,
		)
	})

	// The measured regression: CVE-2026-33817 was the alias of the withdrawn
	// GO-2026-4923, and its package (go.etcd.io/bbolt) had already left go.mod.
	// Alias resolution must not depend on the module graph, or the entry is
	// recorded not-found and the stale suppression survives.
	It("resolves a merged alias whose package is absent from go.mod", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/other v1.0.0\n",
		)
		record := &pkg.OSVVulnerability{
			ID:        "GO-2026-4923",
			Aliases:   []string{"CVE-2026-33817", "GHSA-6jwv-w5xf-7j27"},
			Withdrawn: "2026-04-08T13:33:56Z",
		}
		index := &pkg.GoVulnIndex{
			{ID: "GO-2026-4923", Aliases: []string{"CVE-2026-33817", "GHSA-6jwv-w5xf-7j27"}},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-4923": record}, index,
		))

		osvExpectVerdict(
			classifier, ctx, workdir,
			"CVE-2026-33817", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonWithdrawn,
		)
		osvExpectVerdict(
			classifier, ctx, workdir,
			"GHSA-6jwv-w5xf-7j27", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonWithdrawn,
		)
	})

	It("classifies an advisory whose package is absent as stale/package-absent", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/other v1.0.0\n",
		)
		record := &pkg.OSVVulnerability{
			ID: "GO-2026-0003",
			Affected: []pkg.OSVAffected{
				osvAffectedGo(
					"example.com/missing",
					osvSemverRange(osvIntroduced("0"), osvFixed("2.0.0")),
				),
			},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-0003": record}, &pkg.GoVulnIndex{},
		))

		result := osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0003", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonPackageAbsent,
		)
		Expect(result.Package).To(Equal("example.com/missing"))
	})

	It("classifies an in-use version outside every range as stale", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.0.0\n",
		)
		record := &pkg.OSVVulnerability{
			ID: "GO-2026-0004",
			Affected: []pkg.OSVAffected{
				osvAffectedGo(
					"example.com/dep",
					osvSemverRange(osvIntroduced("2.0.0"), osvFixed("3.0.0")),
				),
			},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-0004": record}, &pkg.GoVulnIndex{},
		))

		result := osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0004", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonVersionNotAffected,
		)
		Expect(result.Package).To(Equal("example.com/dep"))
		Expect(result.InUseVersion).To(Equal("v1.0.0"))
	})

	It("classifies an in-use version inside a range as legitimate", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.5.0\n",
		)
		record := &pkg.OSVVulnerability{
			ID: "GO-2026-0005",
			Affected: []pkg.OSVAffected{
				osvAffectedGo(
					"example.com/dep",
					osvSemverRange(osvIntroduced("1.0.0"), osvFixed("2.0.0")),
				),
			},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-0005": record}, &pkg.GoVulnIndex{},
		))

		result := osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0005", pkg.IgnoreOutcomeLegitimate, pkg.IgnoreReasonVersionAffected,
		)
		Expect(result.Package).To(Equal("example.com/dep"))
		Expect(result.InUseVersion).To(Equal("v1.5.0"))
	})

	DescribeTable("classifies a stdlib advisory against the go directive",
		func(goVersion, outcome, reason string) {
			workdir := osvWorkdir("module example.com/test\n\ngo " + goVersion + "\n")
			record := &pkg.OSVVulnerability{
				ID: "GO-2026-0006",
				Affected: []pkg.OSVAffected{
					osvAffectedGo(
						"stdlib",
						osvSemverRange(osvIntroduced("1.26.0"), osvFixed("1.27.5")),
					),
				},
			}
			classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
				map[string]*pkg.OSVVulnerability{"GO-2026-0006": record}, &pkg.GoVulnIndex{},
			))

			result := osvExpectVerdict(classifier, ctx, workdir, "GO-2026-0006", outcome, reason)
			Expect(result.Package).To(Equal("stdlib"))
			Expect(result.InUseVersion).To(Equal(goVersion))
		},
		Entry("inside the range is legitimate", "1.27.4",
			pkg.IgnoreOutcomeLegitimate, pkg.IgnoreReasonVersionAffected),
		Entry("outside the range is stale", "1.28.0",
			pkg.IgnoreOutcomeStale, pkg.IgnoreReasonVersionNotAffected),
	)

	It("classifies a local-path replace as unresolved/unresolvable-version", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\n" +
				"require example.com/dep v1.0.0\n\nreplace example.com/dep => ../local\n",
		)
		record := &pkg.OSVVulnerability{
			ID: "GO-2026-0007",
			Affected: []pkg.OSVAffected{
				osvAffectedGo(
					"example.com/dep",
					osvSemverRange(osvIntroduced("0"), osvFixed("2.0.0")),
				),
			},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-0007": record}, &pkg.GoVulnIndex{},
		))

		osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0007", pkg.IgnoreOutcomeUnresolved, pkg.IgnoreReasonUnresolvableVersion,
		)
	})

	It("classifies a require version removed by exclude as unresolved", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\n" +
				"require example.com/dep v1.0.0\n\nexclude example.com/dep v1.0.0\n",
		)
		record := &pkg.OSVVulnerability{
			ID: "GO-2026-0008",
			Affected: []pkg.OSVAffected{
				osvAffectedGo(
					"example.com/dep",
					osvSemverRange(osvIntroduced("0"), osvFixed("2.0.0")),
				),
			},
		}
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(
			map[string]*pkg.OSVVulnerability{"GO-2026-0008": record}, &pkg.GoVulnIndex{},
		))

		osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0008", pkg.IgnoreOutcomeUnresolved, pkg.IgnoreReasonUnresolvableVersion,
		)
	})

	It("classifies a 404 with no alias as unresolved/not-found", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.0.0\n",
		)
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(nil, &pkg.GoVulnIndex{}))

		osvExpectVerdict(
			classifier, ctx, workdir,
			"CVE-2026-99999", pkg.IgnoreOutcomeUnresolved, pkg.IgnoreReasonNotFound,
		)
	})

	It("classifies a transport error as unresolved/transport-error", func() {
		workdir := osvWorkdir("module example.com/test\n\ngo 1.24.0\n")
		fake := &mocks.OSVFetcher{}
		fake.FetchVulnStub = func(_ context.Context, _ string) (*pkg.OSVVulnerability, error) {
			return nil, stderrors.New("boom")
		}
		classifier := pkg.NewOSVIgnoreClassifier(fake)

		osvExpectVerdict(
			classifier, ctx, workdir,
			"GO-2026-0010", pkg.IgnoreOutcomeUnresolved, pkg.IgnoreReasonTransportError,
		)
	})

	It("marks every ID unresolved when go.mod cannot be read", func() {
		workdir, err := os.MkdirTemp("", "osv-classifier-missing-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(workdir) })
		classifier := pkg.NewOSVIgnoreClassifier(osvFetcher(nil, &pkg.GoVulnIndex{}))

		results := classifier.Classify(ctx, workdir, []string{"GO-2026-0011", "GO-2026-0012"})
		Expect(results).To(HaveLen(2))
		for _, result := range results {
			Expect(result.Outcome).To(Equal(pkg.IgnoreOutcomeUnresolved))
			Expect(result.Reason).To(Equal(pkg.IgnoreReasonTransportError))
		}
	})
})

var _ = Describe("OSV ignore classifier recheck", func() {
	ctx := context.Background()

	It("keeps a withdrawn verdict regardless of the graph", func() {
		workdir := osvWorkdir("module example.com/test\n\ngo 1.24.0\n")
		stale := []pkg.IgnoreClassification{{
			ID: "GO-2026-0001", Outcome: pkg.IgnoreOutcomeStale, Reason: pkg.IgnoreReasonWithdrawn,
		}}
		Expect(pkg.RecheckStale(ctx, workdir, stale)).To(Equal(stale))
	})

	It("drops a package-absent verdict once the package is present", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.0.0\n",
		)
		stale := []pkg.IgnoreClassification{{
			ID:      "GO-2026-0002",
			Outcome: pkg.IgnoreOutcomeStale,
			Reason:  pkg.IgnoreReasonPackageAbsent,
			Package: "example.com/dep",
		}}
		Expect(pkg.RecheckStale(ctx, workdir, stale)).To(BeEmpty())
	})

	It("keeps a version-not-affected verdict while the version is unchanged", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.0.0\n",
		)
		stale := []pkg.IgnoreClassification{{
			ID:           "GO-2026-0003",
			Outcome:      pkg.IgnoreOutcomeStale,
			Reason:       pkg.IgnoreReasonVersionNotAffected,
			Package:      "example.com/dep",
			InUseVersion: "v1.0.0",
		}}
		Expect(pkg.RecheckStale(ctx, workdir, stale)).To(Equal(stale))
	})

	It("drops a version-not-affected verdict when the version moved", func() {
		workdir := osvWorkdir(
			"module example.com/test\n\ngo 1.24.0\n\nrequire example.com/dep v1.5.0\n",
		)
		stale := []pkg.IgnoreClassification{{
			ID:           "GO-2026-0004",
			Outcome:      pkg.IgnoreOutcomeStale,
			Reason:       pkg.IgnoreReasonVersionNotAffected,
			Package:      "example.com/dep",
			InUseVersion: "v1.0.0",
		}}
		Expect(pkg.RecheckStale(ctx, workdir, stale)).To(BeEmpty())
	})

	It("keeps only withdrawn verdicts when go.mod cannot be read", func() {
		workdir, err := os.MkdirTemp("", "osv-recheck-missing-")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(workdir) })
		stale := []pkg.IgnoreClassification{
			{
				ID: "GO-2026-0005", Outcome: pkg.IgnoreOutcomeStale,
				Reason: pkg.IgnoreReasonWithdrawn,
			},
			{
				ID: "GO-2026-0006", Outcome: pkg.IgnoreOutcomeStale,
				Reason: pkg.IgnoreReasonPackageAbsent, Package: "example.com/dep",
			},
		}
		results := pkg.RecheckStale(ctx, workdir, stale)
		Expect(results).To(HaveLen(1))
		Expect(results[0].ID).To(Equal("GO-2026-0005"))
	})

	It("returns nil for an empty stale list", func() {
		Expect(pkg.RecheckStale(ctx, "/nonexistent", nil)).To(BeNil())
	})
})
