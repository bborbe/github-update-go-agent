// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("FleetScanCommands", func() {
	const (
		repo = "bborbe/ip"
		ref  = "0123456789abcdef"
	)

	both := func(stage string) []pkg.ScanRepoResult {
		return []pkg.ScanRepoResult{{
			Repo:     repo,
			Ref:      ref,
			VulnIDs:  []string{"GO-2026-0001", "CVE-2026-9999"},
			StaleIDs: []string{"GO-2026-0002"},
		}}
	}

	It("produces two commands for a repo carrying both families", func() {
		Expect(pkg.BuildScanCommands("prod", both("prod"))).To(HaveLen(2))
	})

	It("shares the envelope but not the identifier across the two families", func() {
		commands := pkg.BuildScanCommands("prod", both("prod"))
		Expect(commands).To(HaveLen(2))
		vuln, stale := commands[0], commands[1]
		for _, key := range []string{
			"task_type", "assignee", "phase", "status", "stage", "repo", "clone_url", "ref",
		} {
			Expect(vuln.Frontmatter).To(HaveKey(key))
			Expect(vuln.Frontmatter[key]).To(Equal(stale.Frontmatter[key]))
		}
		Expect(vuln.TaskIdentifier).NotTo(Equal(stale.TaskIdentifier))
	})

	It("carries vulns and vuln_count only on the vuln command", func() {
		commands := pkg.BuildScanCommands("prod", both("prod"))
		vuln, stale := commands[0], commands[1]

		Expect(vuln.Frontmatter).To(HaveKeyWithValue(
			"vulns", []string{"GO-2026-0001", "CVE-2026-9999"},
		))
		Expect(vuln.Frontmatter).To(HaveKeyWithValue("vuln_count", 2))

		_, hasVulns := stale.Frontmatter["vulns"]
		Expect(hasVulns).To(BeFalse())
		_, hasCount := stale.Frontmatter["vuln_count"]
		Expect(hasCount).To(BeFalse())
	})

	It("validates every produced command", func() {
		for _, cmd := range pkg.BuildScanCommands("prod", both("prod")) {
			Expect(cmd.Validate(context.Background())).To(BeNil())
		}
	})

	It("never puts a slash in a title", func() {
		for _, cmd := range pkg.BuildScanCommands("prod", both("prod")) {
			Expect(cmd.Title).NotTo(ContainSubstring("/"))
		}
	})

	It("produces one command for a vuln-only result", func() {
		results := []pkg.ScanRepoResult{{
			Repo:    repo,
			Ref:     ref,
			VulnIDs: []string{"GO-2026-0001"},
		}}
		Expect(pkg.BuildScanCommands("", results)).To(HaveLen(1))
	})

	It("produces one command for a stale-only result", func() {
		results := []pkg.ScanRepoResult{{
			Repo:     repo,
			Ref:      ref,
			StaleIDs: []string{"GO-2026-0002"},
		}}
		Expect(pkg.BuildScanCommands("", results)).To(HaveLen(1))
	})

	It("produces no command for a result with neither family", func() {
		results := []pkg.ScanRepoResult{{Repo: repo, Ref: ref}}
		Expect(pkg.BuildScanCommands("prod", results)).To(BeEmpty())
	})

	It("omits stage when empty and includes it when set, in both families", func() {
		withoutStage := pkg.BuildScanCommands("", both(""))
		Expect(withoutStage).To(HaveLen(2))
		for _, cmd := range withoutStage {
			_, ok := cmd.Frontmatter["stage"]
			Expect(ok).To(BeFalse())
		}

		withStage := pkg.BuildScanCommands("prod", both("prod"))
		Expect(withStage).To(HaveLen(2))
		for _, cmd := range withStage {
			Expect(cmd.Frontmatter).To(HaveKeyWithValue("stage", "prod"))
		}
	})

	It("uses the dash-form title and the frozen clone URL", func() {
		commands := pkg.BuildScanCommands("prod", both("prod"))
		Expect(commands[0].Title).To(Equal("Update Go bborbe-ip 0123456"))
		Expect(strings.HasPrefix(commands[1].Title, "Remove stale vuln ignores bborbe-ip ")).
			To(BeTrue())
		Expect(commands[0].Frontmatter).To(
			HaveKeyWithValue("clone_url", "git@github.com:bborbe/ip.git"),
		)
	})
})
