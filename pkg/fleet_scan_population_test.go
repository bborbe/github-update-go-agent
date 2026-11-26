// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

// reposOf renders the repo field of each fact, preserving order.
func reposOf(facts []pkg.ScanCandidateFacts) []string {
	if len(facts) == 0 {
		return nil
	}
	repos := make([]string, 0, len(facts))
	for _, fact := range facts {
		repos = append(repos, fact.Repo)
	}
	return repos
}

var _ = Describe("FleetScanPopulation", func() {
	allowlist := []string{"github.com/bborbe/*", "!github.com/bborbe/go-skeleton"}

	DescribeTable("selectPopulations gates the two populations",
		func(allowlist []string, facts []pkg.ScanCandidateFacts, inVuln, inStale []string) {
			vuln, stale := pkg.SelectPopulations(allowlist, facts)
			Expect(reposOf(vuln)).To(Equal(inVuln))
			Expect(reposOf(stale)).To(Equal(inStale))
		},
		Entry("bborbe/ip allowlisted, go.mod present+parsable, consenting → in both",
			allowlist,
			[]pkg.ScanCandidateFacts{
				{Repo: "bborbe/ip", GoModPresent: true, GoModParsable: true, Consent: true},
			},
			[]string{"bborbe/ip"}, []string{"bborbe/ip"},
		),
		Entry("bborbe/go-skeleton excluded by allowlist '!' → in neither",
			allowlist,
			[]pkg.ScanCandidateFacts{
				{
					Repo:          "bborbe/go-skeleton",
					GoModPresent:  true,
					GoModParsable: true,
					Consent:       true,
				},
			},
			nil, nil,
		),
		Entry("other/repo misses the bborbe wildcard → in neither",
			allowlist,
			[]pkg.ScanCandidateFacts{
				{Repo: "other/repo", GoModPresent: true, GoModParsable: true, Consent: true},
			},
			nil, nil,
		),
		Entry("bborbe/ip without consent → in neither (consent gate)",
			allowlist,
			[]pkg.ScanCandidateFacts{
				{Repo: "bborbe/ip", GoModPresent: true, GoModParsable: true, Consent: false},
			},
			nil, nil,
		),
		Entry("bborbe/ip without go.mod → in neither (go.mod presence gate)",
			allowlist,
			[]pkg.ScanCandidateFacts{
				{Repo: "bborbe/ip", GoModPresent: false, GoModParsable: false, Consent: true},
			},
			nil, nil,
		),
		Entry("bborbe/ip unparsable go.mod → in vuln, not in stale (parsability gate)",
			allowlist,
			[]pkg.ScanCandidateFacts{
				{Repo: "bborbe/ip", GoModPresent: true, GoModParsable: false, Consent: true},
			},
			[]string{"bborbe/ip"}, nil,
		),
		Entry("nil allowlist allows every consenting repo (allow-all within owner)",
			nil,
			[]pkg.ScanCandidateFacts{
				{Repo: "bborbe/ip", GoModPresent: true, GoModParsable: true, Consent: true},
				{Repo: "bborbe/demo", GoModPresent: true, GoModParsable: true, Consent: true},
			},
			[]string{"bborbe/ip", "bborbe/demo"}, []string{"bborbe/ip", "bborbe/demo"},
		),
	)

	DescribeTable("ParseRepoAllowlist splits and trims",
		func(raw string, expected []string) {
			Expect(pkg.ParseRepoAllowlist(raw)).To(Equal(expected))
		},
		Entry("empty", "", nil),
		Entry("whitespace around entries", " a , b ", []string{"a", "b"}),
		Entry(
			"include plus exclude",
			"github.com/bborbe/*,!github.com/bborbe/go-skeleton",
			[]string{"github.com/bborbe/*", "!github.com/bborbe/go-skeleton"},
		),
		Entry("all blank", ",,", nil),
	)
})
