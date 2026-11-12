// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

// replayFixture is one recorded 2026-11-12 plan, captured verbatim from the
// live github-update-go task files before the fix. The population is the 38
// rows carrying GO-2026-6859 (36 populated fixed_version, 2 empty).
type replayFixture struct {
	Source string         `json:"source"`
	Plan   pkg.PlanOutput `json:"plan"`
}

var _ = Describe("reclassifyFixableParks over the recorded 2026-11-12 population", func() {
	It("leaves zero populated-fixed_version findings on the park path", func() {
		raw, err := os.ReadFile(filepath.Join("testdata", "park_replay.json"))
		Expect(err).NotTo(HaveOccurred())

		var fixtures []replayFixture
		Expect(json.Unmarshal(raw, &fixtures)).To(Succeed())
		Expect(fixtures).To(HaveLen(38))

		var reclassified int
		var stillParkedEmpty int
		var stillParkedPopulated int
		for _, f := range fixtures {
			plan := f.Plan
			reclassified += len(pkg.ReclassifyFixableParks(&plan))
			for _, v := range plan.Vulns {
				if v.Action != "park" {
					continue
				}
				if v.FixedVersion == "" {
					stillParkedEmpty++
				} else {
					stillParkedPopulated++
				}
			}
		}

		Expect(stillParkedPopulated).To(Equal(0),
			"a finding with a populated fixed_version must never stay parked")
		Expect(reclassified).To(Equal(42))
		Expect(stillParkedEmpty).To(Equal(2),
			"the two genuinely no-fix findings must still park")
	})
})
