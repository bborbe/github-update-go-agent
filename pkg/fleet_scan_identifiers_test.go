// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("ScanTaskID derivation", func() {
	It("derives the frozen vuln-drift UUID", func() {
		id := pkg.DeriveScanVulnTaskID(
			"bborbe",
			"ip",
			[]string{"GO-2026-0001", "CVE-2026-9999"},
		)
		GinkgoWriter.Printf("vuln UUID: %s\n", id)
		Expect(id).To(Equal("5b8f26d2-bae1-538d-b245-f0a81db06c23"))
	})

	It("derives the frozen stale-ignore UUID", func() {
		id := pkg.DeriveScanStaleIgnoreTaskID(
			"bborbe",
			"ip",
			1,
			[]string{"GO-2026-0002"},
		)
		GinkgoWriter.Printf("stale UUID: %s\n", id)
		Expect(id).To(Equal("064c07cc-2d45-5b18-af93-c4c9b2741f40"))
	})

	It("is order-independent for the vuln list", func() {
		forward := pkg.DeriveScanVulnTaskID(
			"bborbe", "ip", []string{"GO-2026-0001", "CVE-2026-9999"},
		)
		reversed := pkg.DeriveScanVulnTaskID(
			"bborbe", "ip", []string{"CVE-2026-9999", "GO-2026-0001"},
		)
		Expect(reversed).To(Equal(forward))
	})

	It("is deterministic across repeated calls in both families", func() {
		vulnIDs := []string{"GO-2026-0001", "CVE-2026-9999"}
		Expect(pkg.DeriveScanVulnTaskID("bborbe", "ip", vulnIDs)).
			To(Equal(pkg.DeriveScanVulnTaskID("bborbe", "ip", vulnIDs)))
		staleIDs := []string{"GO-2026-0002"}
		Expect(pkg.DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, staleIDs)).
			To(Equal(pkg.DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, staleIDs)))
	})

	It("changes the stale UUID when the generation changes", func() {
		gen1 := pkg.DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, []string{"GO-2026-0002"})
		gen2 := pkg.DeriveScanStaleIgnoreTaskID("bborbe", "ip", 2, []string{"GO-2026-0002"})
		Expect(gen2).NotTo(Equal(gen1))
	})

	It("dedupes the vuln list", func() {
		single := pkg.DeriveScanVulnTaskID("bborbe", "ip", []string{"GO-2026-0001"})
		duplicated := pkg.DeriveScanVulnTaskID(
			"bborbe", "ip", []string{"GO-2026-0001", "GO-2026-0001"},
		)
		Expect(duplicated).To(Equal(single))
	})

	It("does not dedupe the stale list", func() {
		single := pkg.DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, []string{"GO-2026-0002"})
		duplicated := pkg.DeriveScanStaleIgnoreTaskID(
			"bborbe", "ip", 1, []string{"GO-2026-0002", "GO-2026-0002"},
		)
		Expect(duplicated).NotTo(Equal(single))
	})

	It("never collides between the two families for the same repo and ID", func() {
		ids := []string{"GO-2026-0002"}
		Expect(pkg.DeriveScanVulnTaskID("bborbe", "ip", ids)).
			NotTo(Equal(pkg.DeriveScanStaleIgnoreTaskID("bborbe", "ip", 1, ids)))
	})
})
