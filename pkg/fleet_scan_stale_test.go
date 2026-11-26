// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

// classification builds one verdict for the table below.
func classification(id, outcome, reason string) pkg.IgnoreClassification {
	return pkg.IgnoreClassification{ID: id, Outcome: outcome, Reason: reason}
}

var _ = Describe("FleetScanStale", func() {
	DescribeTable(
		"withdrawnOnly keeps only the withdrawn verdict",
		func(classifications []pkg.IgnoreClassification, expected []string) {
			Expect(pkg.WithdrawnOnly(classifications)).To(Equal(expected))
		},
		Entry(
			"stale/withdrawn is kept",
			[]pkg.IgnoreClassification{
				classification("GO-2026-0001", pkg.IgnoreOutcomeStale, pkg.IgnoreReasonWithdrawn),
			},
			[]string{"GO-2026-0001"},
		),
		Entry(
			"stale/package-absent is dropped",
			[]pkg.IgnoreClassification{
				classification(
					"GO-2026-0001",
					pkg.IgnoreOutcomeStale,
					pkg.IgnoreReasonPackageAbsent,
				),
			},
			[]string(nil),
		),
		Entry(
			"stale/version-not-affected is dropped",
			[]pkg.IgnoreClassification{
				classification(
					"GO-2026-0001",
					pkg.IgnoreOutcomeStale,
					pkg.IgnoreReasonVersionNotAffected,
				),
			},
			[]string(nil),
		),
		Entry(
			"legitimate/version-affected is dropped",
			[]pkg.IgnoreClassification{
				classification(
					"GO-2026-0001",
					pkg.IgnoreOutcomeLegitimate,
					pkg.IgnoreReasonVersionAffected,
				),
			},
			[]string(nil),
		),
		Entry(
			"unresolved/transport-error is dropped",
			[]pkg.IgnoreClassification{
				classification(
					"GO-2026-0001",
					pkg.IgnoreOutcomeUnresolved,
					pkg.IgnoreReasonTransportError,
				),
			},
			[]string(nil),
		),
		Entry(
			"unresolved/not-found is dropped",
			[]pkg.IgnoreClassification{
				classification(
					"GO-2026-0001",
					pkg.IgnoreOutcomeUnresolved,
					pkg.IgnoreReasonNotFound,
				),
			},
			[]string(nil),
		),
		Entry(
			"empty input returns nil",
			[]pkg.IgnoreClassification(nil),
			[]string(nil),
		),
		Entry(
			"mixed verdicts keep only withdrawn and sort",
			[]pkg.IgnoreClassification{
				classification(
					"GO-2026-0009",
					pkg.IgnoreOutcomeStale,
					pkg.IgnoreReasonPackageAbsent,
				),
				classification(
					"GO-2026-0005",
					pkg.IgnoreOutcomeStale,
					pkg.IgnoreReasonWithdrawn,
				),
				classification(
					"GO-2026-0007",
					pkg.IgnoreOutcomeStale,
					pkg.IgnoreReasonVersionNotAffected,
				),
				classification(
					"GO-2026-0004",
					pkg.IgnoreOutcomeLegitimate,
					pkg.IgnoreReasonVersionAffected,
				),
				classification(
					"GO-2026-0003",
					pkg.IgnoreOutcomeUnresolved,
					pkg.IgnoreReasonTransportError,
				),
				classification(
					"GO-2026-0001",
					pkg.IgnoreOutcomeStale,
					pkg.IgnoreReasonWithdrawn,
				),
			},
			[]string{"GO-2026-0001", "GO-2026-0005"},
		),
	)

	It("renders every ID and the count", func() {
		ids := []string{"GO-2026-0001", "CVE-2026-9999"}
		body := pkg.RenderStaleBody(ids)
		for _, id := range ids {
			Expect(body).To(ContainSubstring(id))
		}
		Expect(body).To(ContainSubstring(fmt.Sprintf("%d", len(ids))))
		Expect(body).NotTo(ContainSubstring("/"))
	})

	It("stays under 2000 bytes for 20 IDs", func() {
		ids := make([]string, 0, 20)
		for i := 0; i < 20; i++ {
			ids = append(ids, fmt.Sprintf("GO-2026-%04d", i))
		}
		Expect(len(pkg.RenderStaleBody(ids))).To(BeNumerically("<", 2000))
	})
})
