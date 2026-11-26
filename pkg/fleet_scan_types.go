// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"github.com/bborbe/github-update-go-agent/pkg/git"
	"github.com/bborbe/github-update-go-agent/pkg/maintainerconfig"
	libtime "github.com/bborbe/time"
)

// FleetScanDeps is the injected surface of the nightly fleet scan.
type FleetScanDeps struct {
	Config          FleetScanConfig
	GhToken         string `display:"length"`
	Lister          RepoLister
	Files           RepoFileFetcher
	Consent         maintainerconfig.Fetcher
	GitOps          git.GitOps
	Gate            GateRunner
	Classifier      IgnoreClassifier
	CurrentDateTime libtime.CurrentDateTimeGetter
	Store           ScanTaskStore
}

// ScanRepoResult is the per-repository outcome of one successful scan.
type ScanRepoResult struct {
	Repo     string   `json:"repo"`
	Ref      string   `json:"ref"`
	CloneURL string   `json:"clone_url"`
	VulnIDs  []string `json:"vuln_ids,omitempty"`
	StaleIDs []string `json:"stale_ids,omitempty"`
}

// FleetScanReport is the ## Fleet Scan section payload on the scan task.
type FleetScanReport struct {
	ScannedAt       string           `json:"scanned_at"`
	Scanned         int              `json:"scanned"`
	Filed           int              `json:"filed"`
	AlreadyRecorded int              `json:"already_recorded"`
	Repos           []ScanRepoResult `json:"repos,omitempty"`
}
