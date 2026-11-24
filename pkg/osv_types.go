// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

// OSVVulnerability is the subset of an OSV advisory record the classifier reads.
type OSVVulnerability struct {
	ID        string        `json:"id"`
	Aliases   []string      `json:"aliases,omitempty"`
	Withdrawn string        `json:"withdrawn,omitempty"`
	Affected  []OSVAffected `json:"affected,omitempty"`
}

// OSVAffected is one package affected by an advisory.
type OSVAffected struct {
	Package OSVPackage `json:"package"`
	Ranges  []OSVRange `json:"ranges,omitempty"`
}

// OSVPackage names the ecosystem and package an affected entry applies to.
type OSVPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

// OSVRange is one affected-version range for a package.
type OSVRange struct {
	Type   string     `json:"type"`
	Events []OSVEvent `json:"events,omitempty"`
}

// OSVEvent is one boundary in an affected-version range.
type OSVEvent struct {
	Introduced   string `json:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty"`
}

// GoVulnIndex is https://vuln.go.dev/index/vulns.json: module path -> the
// advisories that affect it.
type GoVulnIndex map[string][]GoVulnIndexEntry

// GoVulnIndexEntry is one advisory listed for a module in the Go index.
type GoVulnIndexEntry struct {
	ID       string `json:"id"`
	Modified string `json:"modified,omitempty"`
}

// IgnoreClassification is the verdict for one suppressed advisory ID.
type IgnoreClassification struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
	// Package is the module path the advisory names (or "stdlib"), recorded so
	// execution can re-check local conditions without re-fetching OSV.
	Package string `json:"package,omitempty"`
	// InUseVersion is the version planning classified against, recorded so
	// execution can tell whether the module graph moved.
	InUseVersion string `json:"in_use_version,omitempty"`
}

// The three mutually exclusive outcomes of classifying one ignore.
const (
	IgnoreOutcomeStale      = "stale"
	IgnoreOutcomeLegitimate = "legitimate"
	IgnoreOutcomeUnresolved = "unresolved"
)

// The reason accompanying an IgnoreClassification outcome.
const (
	IgnoreReasonWithdrawn           = "withdrawn"
	IgnoreReasonPackageAbsent       = "package-absent"
	IgnoreReasonVersionNotAffected  = "version-not-affected"
	IgnoreReasonVersionAffected     = "version-affected"
	IgnoreReasonNotGo               = "not-go"
	IgnoreReasonNotFound            = "not-found"
	IgnoreReasonTransportError      = "transport-error"
	IgnoreReasonBudgetExceeded      = "budget-exceeded"
	IgnoreReasonUnresolvableVersion = "unresolvable-version"
)
