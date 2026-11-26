// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"strings"

	"github.com/bborbe/maintainer/repoallowlist"
)

// fleetHost is the host component of every allowlist target the scan renders.
const fleetHost = "github.com"

// FleetScanConfig is the deployment-supplied scan scope.
type FleetScanConfig struct {
	Owner     string   // OWNER — the owner whose repositories the lister enumerates
	Allowlist []string // REPO_ALLOWLIST — host/owner/repo entries; '*' globs, '!' excludes
	Stage     string   // STAGE — stamped on every task the scan files
}

// ParseRepoAllowlist splits a comma-separated REPO_ALLOWLIST value into
// trimmed, non-empty entries. An empty or all-blank value returns nil, which
// repoallowlist reads as allow-all within the owner.
func ParseRepoAllowlist(raw string) []string {
	parts := strings.Split(raw, ",")
	entries := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		entries = append(entries, part)
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}

// ScanCandidateFacts is one allowlist-visible repository and its gate facts.
type ScanCandidateFacts struct {
	Repo          string // "owner/name"
	GoModPresent  bool
	GoModParsable bool
	Consent       bool
}

// selectPopulations partitions facts into the two scan populations.
//
// vuln  = allowlist ∩ GoModPresent ∩ Consent
// stale = allowlist ∩ GoModPresent ∩ GoModParsable ∩ Consent
//
// Allowlist membership is decided with repoallowlist.IsAllowed over
// scanTarget(facts.Repo). Both slices preserve the input order.
func selectPopulations(
	allowlist []string,
	facts []ScanCandidateFacts,
) (vuln, stale []ScanCandidateFacts) {
	for _, fact := range facts {
		if !repoallowlist.IsAllowed(allowlist, scanTarget(fact.Repo)) {
			continue
		}
		if !fact.GoModPresent || !fact.Consent {
			continue
		}
		vuln = append(vuln, fact)
		if fact.GoModParsable {
			stale = append(stale, fact)
		}
	}
	return vuln, stale
}

// scanTarget renders the allowlist target form for a repo: "github.com/<repo>".
func scanTarget(repo string) string {
	return fleetHost + "/" + repo
}
