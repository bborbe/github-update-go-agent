// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// scanVulnNamespace is the retired github-vuln-watcher's frozen v5 UUID
// namespace for vuln-drift task identifiers. Never mint a new one: an existing
// open task must keep its identifier across the migration.
const scanVulnNamespace = "5c3bcb6b-fb0f-4c61-a4c3-8a17fd037f52"

// scanStaleNamespace is the retired github-update-go-watcher's frozen v5 UUID
// namespace for stale-ignore task identifiers.
const scanStaleNamespace = "4f2a7c1e-9b3d-4c58-8e21-6d0b7a5c9f13"

// scanStaleGeneration is the generation the nightly scan files at. The frozen
// seed carries a generation so a future re-filing scheme can mint a new
// identifier without colliding; the nightly run always uses 1.
const scanStaleGeneration = 1

var (
	scanVulnNamespaceUUID  = uuid.MustParse(scanVulnNamespace)
	scanStaleNamespaceUUID = uuid.MustParse(scanStaleNamespace)
)

// DeriveScanVulnTaskID returns the deterministic identifier for a repo's
// vulnerability task. The seed is the retired watcher's verbatim:
//
//	vuln-drift-<owner>-<repo>-<comma-joined deduped sorted ids>
//
// A reordered or repeated advisory list yields the same identifier.
func DeriveScanVulnTaskID(owner, repo string, ids []string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	sorted = slices.Compact(sorted)
	seed := "vuln-drift-" + owner + "-" + repo + "-" + strings.Join(sorted, ",")
	return uuid.NewSHA1(scanVulnNamespaceUUID, []byte(seed)).String()
}

// DeriveScanStaleIgnoreTaskID returns the deterministic identifier for a
// repo's stale-ignore task. The seed is the retired watcher's verbatim:
//
//	stale-ignore-<owner>-<repo>-g<generation>-<comma-joined sorted ids>
//
// The list is sorted but NOT deduped — deduping would change the identifier of
// an already-filed item.
func DeriveScanStaleIgnoreTaskID(owner, repo string, generation int, ids []string) string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	seed := "stale-ignore-" + owner + "-" + repo + "-g" +
		strconv.Itoa(generation) + "-" + strings.Join(sorted, ",")
	return uuid.NewSHA1(scanStaleNamespaceUUID, []byte(seed)).String()
}
