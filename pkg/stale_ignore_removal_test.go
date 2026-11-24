// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg"
)

// seededRepo writes a temporary git repository whose three ignore surfaces
// carry both a stale ID and a legitimate one, then commits it so the removal
// produces a real git diff.
func seededRepo() string {
	dir, err := os.MkdirTemp("", "stale-ignore-seed-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })

	files := map[string]string{
		"go.mod": "module example.com/test\n\ngo 1.24.0\n\n" +
			"require example.com/keep v1.0.0\n",
		"Makefile":           "VULNCHECK_IGNORE ?= CVE-2026-33817 \\\n\tGO-2026-9999\n",
		"Makefile.precommit": "VULNCHECK_IGNORE ?= CVE-2026-33817 GO-2026-9999\n",
		".trivyignore": "# withdrawn bbolt advisory\n" +
			"CVE-2026-33817\n\n" +
			"# legitimate keep advisory\n" +
			"GO-2026-9999\n",
		".osv-scanner.toml": "[[IgnoredVulns]]\n" +
			"id = \"CVE-2026-33817\"\n" +
			"reason = \"withdrawn bbolt\"\n\n" +
			"[[IgnoredVulns]]\n" +
			"id = \"GO-2026-9999\"\n" +
			"reason = \"legitimate keep\"\n",
	}
	for name, content := range files {
		Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)).To(Succeed())
	}
	runGit(dir, "init", "-q")
	runGit(dir, "add", "-A")
	runGit(dir, "-c", "user.name=seed", "-c", "user.email=seed@example.com",
		"commit", "-q", "-m", "seed")
	return dir
}

// runGit runs one git command in dir, failing the spec on a non-zero exit.
func runGit(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(out))
	return string(out)
}

// readFile returns a seeded file's content.
func readFile(dir, name string) string {
	content, err := os.ReadFile(filepath.Join(dir, name))
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

func staleWithdrawn(id string) pkg.IgnoreClassification {
	return pkg.IgnoreClassification{
		ID: id, Outcome: pkg.IgnoreOutcomeStale, Reason: pkg.IgnoreReasonWithdrawn,
	}
}

func legitimateAffected(id string) pkg.IgnoreClassification {
	return pkg.IgnoreClassification{
		ID: id, Outcome: pkg.IgnoreOutcomeLegitimate, Reason: pkg.IgnoreReasonVersionAffected,
	}
}

var _ = Describe("Stale ignore removal", func() {
	ctx := context.Background()

	It("removes the stale ID from all three surfaces and keeps the legitimate one", func() {
		dir := seededRepo()

		removed, err := pkg.RemoveStaleIgnores(ctx, dir, []pkg.IgnoreClassification{
			staleWithdrawn("CVE-2026-33817"),
			legitimateAffected("GO-2026-9999"),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(Equal([]string{"CVE-2026-33817"}))

		diff := runGit(dir, "diff")
		GinkgoWriter.Println("produced diff:\n" + diff)

		trivy := readFile(dir, ".trivyignore")
		Expect(trivy).NotTo(ContainSubstring("CVE-2026-33817"))
		Expect(trivy).To(ContainSubstring("GO-2026-9999"))
		// The comment that documented only the removed ID is gone with it.
		Expect(trivy).NotTo(ContainSubstring("withdrawn bbolt advisory"))

		toml := readFile(dir, ".osv-scanner.toml")
		Expect(toml).NotTo(ContainSubstring("CVE-2026-33817"))
		Expect(toml).To(ContainSubstring("GO-2026-9999"))
		Expect(toml).To(ContainSubstring(`reason = "legitimate keep"`))
		Expect(toml).NotTo(ContainSubstring(`reason = "withdrawn bbolt"`))

		makefile := readFile(dir, "Makefile")
		Expect(makefile).NotTo(ContainSubstring("CVE-2026-33817"))
		Expect(makefile).To(ContainSubstring("GO-2026-9999"))

		precommit := readFile(dir, "Makefile.precommit")
		Expect(precommit).NotTo(ContainSubstring("CVE-2026-33817"))
		Expect(precommit).To(ContainSubstring("GO-2026-9999"))

		// Negative evidence: the legitimate ID is never removed.
		Expect(diff).NotTo(ContainSubstring("-GO-2026-9999"))
		Expect(diff).To(ContainSubstring("-CVE-2026-33817"))
	})

	It("changes no byte when the stale list is empty", func() {
		dir := seededRepo()
		before := snapshot(dir)

		removed, err := pkg.RemoveStaleIgnores(ctx, dir, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
		Expect(snapshot(dir)).To(Equal(before))
		Expect(runGit(dir, "diff")).To(BeEmpty())
	})

	It("changes no byte when only legitimate and unresolved verdicts exist", func() {
		dir := seededRepo()
		before := snapshot(dir)

		removed, err := pkg.RemoveStaleIgnores(ctx, dir, []pkg.IgnoreClassification{
			legitimateAffected("GO-2026-9999"),
			{ID: "CVE-2026-0000", Outcome: pkg.IgnoreOutcomeUnresolved,
				Reason: pkg.IgnoreReasonNotFound},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
		Expect(snapshot(dir)).To(Equal(before))
	})

	It("drops a package-absent verdict when the package is present at execution", func() {
		dir := seededRepo()
		before := snapshot(dir)

		removed, err := pkg.RemoveStaleIgnores(ctx, dir, []pkg.IgnoreClassification{{
			ID: "GO-2026-8888", Outcome: pkg.IgnoreOutcomeStale,
			Reason: pkg.IgnoreReasonPackageAbsent, Package: "example.com/keep",
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(BeEmpty())
		Expect(snapshot(dir)).To(Equal(before))
	})

	It("removes only the stale line of a shared trivy comment block", func() {
		dir := seededRepo()
		Expect(os.WriteFile(
			filepath.Join(dir, ".trivyignore"),
			[]byte("# two advisories\nCVE-2026-33817\nGO-2026-9999\n"), 0o644,
		)).To(Succeed())

		removed, err := pkg.RemoveStaleIgnores(ctx, dir, []pkg.IgnoreClassification{
			staleWithdrawn("CVE-2026-33817"),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(Equal([]string{"CVE-2026-33817"}))
		trivy := readFile(dir, ".trivyignore")
		Expect(trivy).To(ContainSubstring("# two advisories"))
		Expect(trivy).NotTo(ContainSubstring("CVE-2026-33817"))
		Expect(trivy).To(ContainSubstring("GO-2026-9999"))
	})

	It("repairs a continuation line emptied by the removal", func() {
		dir := seededRepo()
		Expect(os.WriteFile(
			filepath.Join(dir, "Makefile"),
			[]byte("VULNCHECK_IGNORE ?= GO-2026-9999 \\\n\tCVE-2026-33817\n"), 0o644,
		)).To(Succeed())

		removed, err := pkg.RemoveStaleIgnores(ctx, dir, []pkg.IgnoreClassification{
			staleWithdrawn("CVE-2026-33817"),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(removed).To(Equal([]string{"CVE-2026-33817"}))
		makefile := readFile(dir, "Makefile")
		Expect(makefile).To(ContainSubstring("VULNCHECK_IGNORE ?= GO-2026-9999"))
		Expect(makefile).NotTo(ContainSubstring("CVE-2026-33817"))
		Expect(makefile).NotTo(ContainSubstring("\\"))
	})
})

// snapshot captures the seeded ignore surfaces for byte-identity assertions.
func snapshot(dir string) map[string]string {
	return map[string]string{
		"Makefile":           readFile(dir, "Makefile"),
		"Makefile.precommit": readFile(dir, "Makefile.precommit"),
		".trivyignore":       readFile(dir, ".trivyignore"),
		".osv-scanner.toml":  readFile(dir, ".osv-scanner.toml"),
	}
}

var _ = Describe("Stale ignore counts as work", func() {
	stalePlan := func() *pkg.PlanOutput {
		return &pkg.PlanOutput{
			Outcome: "ready",
			HasWork: true,
			IgnoreClassifications: []pkg.IgnoreClassification{
				staleWithdrawn("CVE-2026-33817"),
			},
		}
	}

	DescribeTable("a stale-ignore-only plan is work under every scope",
		func(scope pkg.UpdateScope) {
			Expect(pkg.PlanHasWorkForScope(stalePlan(), scope)).To(BeTrue())
		},
		Entry("both", pkg.UpdateScopeBoth),
		Entry("golang", pkg.UpdateScopeGolang),
		Entry("deps", pkg.UpdateScopeDeps),
	)

	It("does not count legitimate or unresolved verdicts as work", func() {
		plan := &pkg.PlanOutput{
			IgnoreClassifications: []pkg.IgnoreClassification{
				legitimateAffected("GO-2026-9999"),
				{ID: "CVE-2026-0000", Outcome: pkg.IgnoreOutcomeUnresolved},
			},
		}
		Expect(pkg.PlanHasWorkForScope(plan, pkg.UpdateScopeBoth)).To(BeFalse())
	})
})
