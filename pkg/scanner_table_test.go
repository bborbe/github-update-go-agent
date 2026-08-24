// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("parseScannerOutput", func() {
	It("parses the osv-scanner shape", func() {
		rows := pkg.ParseScannerOutput("check",
			"GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6\n")
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal("GO-2026-1234"))
		Expect(rows[0].Package).To(Equal("stdlib"))
		Expect(rows[0].FixedVersion).To(Equal("1.26.6"))
		Expect(rows[0].Scanner).To(Equal("osv-scanner"))
	})

	It("parses the govulncheck shape", func() {
		rows := pkg.ParseScannerOutput(
			"vulncheck",
			"GO-2026-5932\tgolang.org/x/crypto/openpgp@v0.0.0-20241113183425-a8a1ce24caf7 -> v0.38.0\tOpenPGP default weak\n",
		)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal("GO-2026-5932"))
		Expect(rows[0].Package).To(Equal("golang.org/x/crypto/openpgp"))
		Expect(rows[0].FixedVersion).To(Equal("v0.38.0"))
		Expect(rows[0].Scanner).To(Equal("govulncheck"))
	})

	It("parses the trivy shape", func() {
		rows := pkg.ParseScannerOutput("trivy",
			"golang.org/x/net │ GO-2026-9998 │ HIGH │ 0.32.0 │ 0.36.0 │ summary\n")
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal("GO-2026-9998"))
		Expect(rows[0].Package).To(Equal("golang.org/x/net"))
		Expect(rows[0].FixedVersion).To(Equal("0.36.0"))
		Expect(rows[0].Scanner).To(Equal("trivy"))
	})

	It("parses the trivy shape with the Status column (real trivy ≥ 0.5x output)", func() {
		// trivy's table gained a Status cell between Severity and Installed
		// Version. The fixed version now sits one cell further right; a
		// parser still assuming the old layout reads the Installed version
		// as the fix (regression: golang.org/x/mod v0.37.0 reported as
		// fixed at v0.39.0, so the model's targeted go get was a no-op and
		// CI stayed red — GO-2026-6179/6180, CVE-2026-56864/56865).
		rows := pkg.ParseScannerOutput(
			"trivy",
			"golang.org/x/mod │ CVE-2026-56864 │ HIGH │ fixed │ v0.39.0 │ 0.40.0 │ A malicious GOSUMDB was capable of serving arbitrary module\n",
		)
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal("CVE-2026-56864"))
		Expect(rows[0].Package).To(Equal("golang.org/x/mod"))
		Expect(rows[0].FixedVersion).To(Equal("0.40.0"))
		Expect(rows[0].Scanner).To(Equal("trivy"))
	})

	It("returns an empty table for empty output", func() {
		Expect(pkg.ParseScannerOutput("check", "")).To(BeEmpty())
	})

	It("skips lines without an advisory ID", func() {
		rows := pkg.ParseScannerOutput("check",
			"make: Entering directory '/tmp/x'\nNo unignored vulnerabilities found\n")
		Expect(rows).To(BeEmpty())
	})

	It("falls back to a target-derived scanner for an unknown shape", func() {
		rows := pkg.ParseScannerOutput("precommit", "GO-2026-9999 somewhere\n")
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal("GO-2026-9999"))
		Expect(rows[0].Package).To(Equal(""))
		Expect(rows[0].FixedVersion).To(Equal(""))
		Expect(rows[0].Scanner).To(Equal("precommit"))
	})

	It("maps a vulncheck-target fallback to govulncheck", func() {
		rows := pkg.ParseScannerOutput("vulncheck", "GO-2026-9999\n")
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].Scanner).To(Equal("govulncheck"))
	})

	It("maps osv-scanner and trivy target fallbacks", func() {
		Expect(
			pkg.ParseScannerOutput("osv-scanner", "GO-2026-9999\n")[0].Scanner,
		).To(Equal("osv-scanner"))
		Expect(pkg.ParseScannerOutput("trivy", "GO-2026-9999\n")[0].Scanner).To(Equal("trivy"))
	})

	It("captures a full ID and never truncates to its prefix", func() {
		rows := pkg.ParseScannerOutput("check", "GO-2026-50260 | stdlib | 1.26.5 | fixed 1.26.6\n")
		Expect(rows).To(HaveLen(1))
		Expect(rows[0].ID).To(Equal("GO-2026-50260"))
	})

	It("skips non-ID noise and accumulates rows across the full captured output", func() {
		raw := "make: Entering directory '/tmp/x'\n" +
			"GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6\n" +
			"No unignored vulnerabilities found\n" +
			"GO-2026-5932\tgolang.org/x/crypto/openpgp@v0.0.0-20241113183425-a8a1ce24caf7 -> v0.38.0\tOpenPGP default weak\n" +
			"make: Leaving directory '/tmp/x'\n"
		rows := pkg.ParseScannerOutput("check", raw)
		Expect(rows).To(HaveLen(2))
		Expect(rows[0].ID).To(Equal("GO-2026-1234"))
		Expect(rows[1].ID).To(Equal("GO-2026-5932"))
	})
})

var _ = Describe("detectGateTargets", func() {
	var (
		ctx     context.Context
		workdir string
	)
	writeMakefile := func(name, content string) {
		Expect(os.WriteFile(filepath.Join(workdir, name), []byte(content), 0o644)).To(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		workdir, err = os.MkdirTemp("", "scanner-table-test-")
		Expect(err).To(BeNil())
	})
	AfterEach(func() {
		Expect(os.RemoveAll(workdir)).To(Succeed())
	})

	It("returns check+vulncheck in preference order when both are defined", func() {
		writeMakefile("Makefile", ".PHONY: check vulncheck\ncheck:\n\t@:\nvulncheck:\n\t@:\n")
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(targets).To(Equal([]string{"check", "vulncheck"}))
	})

	It("returns precommit when only it is defined", func() {
		writeMakefile("Makefile", "precommit: lint test\n\t@echo done\n")
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(targets).To(Equal([]string{"precommit"}))
	})

	It("does not count a known target inside a recipe line", func() {
		writeMakefile("Makefile", "build:\n\tcheck:\n\t@echo hi\n")
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(targets).To(BeNil())
	})

	It("returns nil when none of the known targets are defined", func() {
		writeMakefile("Makefile", "build:\n\t@echo hi\n")
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(targets).To(BeNil())
	})

	It("returns nil when the Makefile is missing", func() {
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(targets).To(BeNil())
	})

	It("detects a target defined only in Makefile.precommit", func() {
		writeMakefile("Makefile.precommit", "vulncheck:\n\t@echo scan\n")
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(targets).To(Equal([]string{"vulncheck"}))
	})

	It("wraps a non-IsNotExist Makefile read failure", func() {
		// A directory is unreadable as a file — a real error, not IsNotExist.
		Expect(os.Mkdir(filepath.Join(workdir, "Makefile"), 0o755)).To(Succeed())
		targets, err := pkg.DetectGateTargets(ctx, workdir)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("read Makefile"))
		Expect(targets).To(BeNil())
	})
})

var _ = Describe("validatePlanAgainstTable", func() {
	var ctx = context.Background()

	It("returns nil when every plan ID is in the table", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-1234"}}
		plan := &pkg.PlanOutput{Vulns: []pkg.PlanVuln{{ID: "GO-2026-1234"}}}
		Expect(pkg.ValidatePlanAgainstTable(ctx, plan, table)).To(BeNil())
	})

	It("errors naming a plan ID absent from the table", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-1234"}}
		plan := &pkg.PlanOutput{Vulns: []pkg.PlanVuln{{ID: "GO-2025-3283"}}}
		err := pkg.ValidatePlanAgainstTable(ctx, plan, table)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("GO-2025-3283"))
	})

	It("errors on a prefix-collision ID (exact match, not prefix)", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-5026"}}
		plan := &pkg.PlanOutput{Vulns: []pkg.PlanVuln{{ID: "GO-2026-50260"}}}
		err := pkg.ValidatePlanAgainstTable(ctx, plan, table)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring("GO-2026-50260"))
	})
})

var _ = Describe("ScannerTable", func() {
	It("Contains and Row match IDs exactly", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-5026"}}
		Expect(table.Contains("GO-2026-5026")).To(BeTrue())
		Expect(table.Contains("GO-2026-50260")).To(BeFalse())

		row, ok := table.Row("GO-2026-5026")
		Expect(ok).To(BeTrue())
		Expect(row.ID).To(Equal("GO-2026-5026"))

		_, ok = table.Row("GO-2026-50260")
		Expect(ok).To(BeFalse())
	})

	It("RenderScannerTable renders one row per finding", func() {
		table := pkg.ScannerTable{
			{ID: "GO-2026-1234", Package: "stdlib", FixedVersion: "1.26.6", Scanner: "osv-scanner"},
			{ID: "CVE-2026-9999", Scanner: "govulncheck"},
		}
		Expect(pkg.RenderScannerTable(table)).To(Equal(
			"GO-2026-1234 | stdlib | 1.26.6 | osv-scanner\nCVE-2026-9999 |  |  | govulncheck",
		))
	})
})

var _ = Describe("loadSuppressedVulnIDs", func() {
	var (
		ctx     context.Context
		workdir string
	)
	writeFile := func(name, content string) {
		Expect(os.WriteFile(filepath.Join(workdir, name), []byte(content), 0o644)).To(Succeed())
	}

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		workdir, err = os.MkdirTemp("", "suppressed-test-")
		Expect(err).To(BeNil())
	})
	AfterEach(func() {
		Expect(os.RemoveAll(workdir)).To(Succeed())
	})

	It("returns an empty set when no suppression surface exists", func() {
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(BeEmpty())
	})

	It("reads [[IgnoredVulns]] ids from .osv-scanner.toml", func() {
		writeFile(".osv-scanner.toml", `
[[IgnoredVulns]]
id = "GO-2026-5064"
reason = "docker indirect, no fix"

[[IgnoredVulns]]
id = "GHSA-pxq6-2prw-chj9"
reason = "no fix"
`)
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(HaveKey("GO-2026-5064"))
		Expect(suppressed).To(HaveKey("GHSA-pxq6-2prw-chj9"))
	})

	It("reads one ID per line from .trivyignore with comments", func() {
		writeFile(".trivyignore", `# fleet no-fix
GO-2026-5932 # x/crypto no fix
CVE-2026-56864
`)
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(HaveKey("GO-2026-5932"))
		Expect(suppressed).To(HaveKey("CVE-2026-56864"))
	})

	It("reads VULNCHECK_IGNORE from Makefile", func() {
		writeFile("Makefile", "VULNCHECK_IGNORE ?= GO-2026-4923 GO-2026-5338\n")
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(HaveKey("GO-2026-4923"))
		Expect(suppressed).To(HaveKey("GO-2026-5338"))
	})

	It("reads VULNCHECK_IGNORE from Makefile.precommit", func() {
		writeFile("Makefile.precommit", "VULNCHECK_IGNORE ?= GO-2026-5622\n")
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(HaveKey("GO-2026-5622"))
	})

	It("accumulates VULNCHECK_IGNORE across continuation lines", func() {
		writeFile("Makefile", "VULNCHECK_IGNORE ?= GO-2026-4923 GO-2026-5338 \\\n\tGO-2026-5622\n")
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(HaveKey("GO-2026-4923"))
		Expect(suppressed).To(HaveKey("GO-2026-5338"))
		Expect(suppressed).To(HaveKey("GO-2026-5622"))
	})

	It("does not misread a longer ID as its prefix in the same surface", func() {
		// GO-2026-50260 present in the same Makefile list as GO-2026-5026: the
		// regex must capture each verbatim, never truncate the longer ID to its
		// shorter prefix (spec: exact ID matching, never substring).
		writeFile("Makefile", "VULNCHECK_IGNORE ?= GO-2026-5026 GO-2026-50260\n")
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).To(BeNil())
		Expect(suppressed).To(HaveKey("GO-2026-5026"))
		Expect(suppressed).To(HaveKey("GO-2026-50260"))
	})

	It("errors on an unreadable suppression surface", func() {
		Expect(os.Mkdir(filepath.Join(workdir, ".trivyignore"), 0o755)).To(Succeed())
		suppressed, err := pkg.LoadSuppressedVulnIDs(ctx, workdir)
		Expect(err).NotTo(BeNil())
		Expect(err.Error()).To(ContainSubstring(".trivyignore"))
		Expect(suppressed).To(BeNil())
	})
})

var _ = Describe("ScannerTable.FilterSuppressed", func() {
	It("returns the table unchanged when suppressed is empty", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-1234"}, {ID: "GO-2026-5932"}}
		Expect(table.FilterSuppressed(nil)).To(Equal(table))
	})

	It("drops rows whose ID is suppressed", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-1234"}, {ID: "GO-2026-5932"}}
		filtered := table.FilterSuppressed(map[string]bool{"GO-2026-5932": true})
		Expect(filtered).To(HaveLen(1))
		Expect(filtered[0].ID).To(Equal("GO-2026-1234"))
	})

	It("keeps a longer ID when only its prefix is suppressed (exact match)", func() {
		table := pkg.ScannerTable{{ID: "GO-2026-50260"}, {ID: "GO-2026-5026"}}
		filtered := table.FilterSuppressed(map[string]bool{"GO-2026-5026": true})
		Expect(filtered).To(HaveLen(1))
		Expect(filtered[0].ID).To(Equal("GO-2026-50260"))
	})
})

var _ = Describe("parkMessage", func() {
	It("carries the verbatim scanner row and the three suppression surfaces", func() {
		table := pkg.ScannerTable{
			{ID: "GO-2026-5932", Scanner: "govulncheck", FixedVersion: "v0.38.0"},
		}
		msg := pkg.ParkMessage(
			[]pkg.PlanVuln{{ID: "GO-2026-5932", Action: "park", Reason: "no upstream fix"}},
			table,
		)
		Expect(
			msg,
		).To(ContainSubstring("GO-2026-5932 (scanner=govulncheck, fixed_version=v0.38.0)"))
		Expect(msg).To(ContainSubstring("VULNCHECK_IGNORE"))
		Expect(msg).To(ContainSubstring(".osv-scanner.toml"))
		Expect(msg).To(ContainSubstring(".trivyignore"))
		Expect(msg).NotTo(ContainSubstring("no upstream fix"))
	})

	It("falls back to the plan vuln's scanner when the row is missing from the table", func() {
		msg := pkg.ParkMessage(
			[]pkg.PlanVuln{{ID: "GO-9999-0001", Action: "park", Scanner: "trivy"}},
			pkg.ScannerTable{},
		)
		Expect(msg).To(ContainSubstring("GO-9999-0001 (scanner=trivy, fixed_version=)"))
		Expect(msg).To(ContainSubstring("VULNCHECK_IGNORE"))
	})
})
