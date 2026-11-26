// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// recheckStale returns the stale classifications that still qualify against
// workdir's go.mod. A withdrawn verdict is taken from the plan. A
// package-absent verdict is dropped when the package is now present. A
// version-not-affected verdict is dropped when the module is now absent, is
// unresolvable, or its in-use version differs from the one planning recorded.
// A go.mod that cannot be read keeps only the withdrawn verdicts (remove
// nothing that depends on local state). It never returns an error.
func recheckStale(
	ctx context.Context,
	workdir string,
	stale []IgnoreClassification,
) []IgnoreClassification {
	if len(stale) == 0 {
		return nil
	}

	versions, unresolvable, goDirective, err := loadModuleGraph(workdir)
	if err != nil {
		glog.V(2).Infof(
			"event=osv_recheck_graph_error err=%v",
			errors.Wrapf(ctx, err, "load module graph for recheck"),
		)
		return withdrawnClassifications(stale)
	}

	kept := make([]IgnoreClassification, 0, len(stale))
	for _, c := range stale {
		select {
		case <-ctx.Done():
			return kept
		default:
		}
		if recheckQualifies(c, versions, unresolvable, goDirective) {
			kept = append(kept, c)
		}
	}
	return kept
}

// withdrawnClassifications returns only the withdrawn verdicts. It is the
// graph-failure fallback: a withdrawn advisory needs no local re-check.
func withdrawnClassifications(stale []IgnoreClassification) []IgnoreClassification {
	kept := make([]IgnoreClassification, 0, len(stale))
	for _, c := range stale {
		if c.Reason == IgnoreReasonWithdrawn {
			kept = append(kept, c)
		}
	}
	return kept
}

// recheckQualifies decides whether one stale verdict still holds. An unknown
// reason is dropped: the caller removes only what it can prove.
func recheckQualifies(
	c IgnoreClassification,
	versions map[string]string,
	unresolvable map[string]bool,
	goDirective string,
) bool {
	switch c.Reason {
	case IgnoreReasonWithdrawn:
		return true
	case IgnoreReasonPackageAbsent:
		if c.Package == "" {
			return true
		}
		_, present := moduleForPath(versions, c.Package)
		return !present
	case IgnoreReasonVersionNotAffected:
		return recheckVersionNotAffected(c, versions, unresolvable, goDirective)
	default:
		return false
	}
}

func recheckVersionNotAffected(
	c IgnoreClassification,
	versions map[string]string,
	unresolvable map[string]bool,
	goDirective string,
) bool {
	if c.Package == "stdlib" {
		return c.InUseVersion != "" && goDirective == c.InUseVersion
	}
	if c.Package == "" {
		return false
	}
	module, ok := moduleForPath(versions, c.Package)
	if !ok {
		return false
	}
	if unresolvable[module] {
		return false
	}
	return versions[module] == c.InUseVersion
}
