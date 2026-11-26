// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"fmt"
	"sort"
	"strings"
)

// withdrawnOnly returns the IDs whose classification says the advisory was
// withdrawn, sorted. Every other outcome — legitimate, unresolved — and every
// other stale reason — package absent, version not affected — is dropped: the
// scan files a removal task only for an advisory upstream has withdrawn.
func withdrawnOnly(classifications []IgnoreClassification) []string {
	var ids []string
	for _, c := range classifications {
		if c.Outcome == IgnoreOutcomeStale && c.Reason == IgnoreReasonWithdrawn {
			ids = append(ids, c.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// renderStaleBody renders the operator-readable body of a stale-ignore task.
// It names the count, lists every advisory ID, and states that the scan removed
// nothing — the update lane's in-PR classifier decides the removal. Keep it
// under a few hundred bytes; the controller caps a body at 500 KiB.
func renderStaleBody(ids []string) string {
	var b strings.Builder
	fmt.Fprintf(
		&b,
		"%d suppressed advisory ID(s) were withdrawn upstream and remain ignored:\n\n",
		len(ids),
	)
	for _, id := range ids {
		fmt.Fprintf(&b, "- %s\n", id)
	}
	b.WriteString(
		"\nThe nightly scan removed nothing; the update lane's in-PR classifier decides the removal.\n",
	)
	return b.String()
}
