// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
)

// parseVulnsList reads the task frontmatter's optional `vulns` list — written
// by `github-vuln-watcher` on every vuln task — and validates it.
//
// (nil, nil) means the task is not vuln-scoped: the field is absent, null, or
// an empty list, and the ordinary update path runs unchanged. A non-nil error
// means the list is present but malformed; the caller fails closed (planning
// escalates needs_input, execution fails the run) rather than treating it as
// absent, because "absent" means "run the full sweep" — the exact behaviour a
// vuln task exists to avoid.
//
// `vuln_count` is informational and deliberately never read: `vulns` is the
// sole scope signal, so a watcher that stops writing the count changes
// nothing here.
func parseVulnsList(ctx context.Context, md *agentlib.Markdown) ([]string, error) {
	raw, ok := md.Frontmatter["vulns"]
	if !ok || raw == nil {
		return nil, nil
	}
	items, ok := vulnsItems(raw)
	if !ok {
		return nil, invalidVulns(
			ctx,
			raw,
			"must be a list of advisory-ID strings",
		)
	}
	if len(items) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx, ctx.Err(), "parse vulns list")
		default:
		}
		value, ok := item.(string)
		if !ok {
			return nil, invalidVulns(ctx, raw, "every element must be a string")
		}
		value = strings.TrimSpace(value)
		if !advisoryIDRegexp.MatchString(value) {
			return nil, invalidVulns(
				ctx,
				raw,
				"element "+value+" must match the advisory-ID shape "+
					"(GO-<year>-<n>, CVE-<year>-<n>, GHSA-xxxx-xxxx-xxxx)",
			)
		}
		ids = append(ids, value)
	}
	return ids, nil
}

// vulnsItems narrows the raw `vulns` frontmatter entry to a sequence. yaml.v3
// decodes a sequence into []interface{}; []string is accepted too so the
// shape check never depends on that decoding detail. Anything else — a
// scalar, a mapping, a nested list — is not the watcher's list contract.
func vulnsItems(raw interface{}) ([]interface{}, bool) {
	switch v := raw.(type) {
	case []interface{}:
		return v, true
	case []string:
		items := make([]interface{}, len(v))
		for i, s := range v {
			items[i] = s
		}
		return items, true
	default:
		return nil, false
	}
}

// invalidVulns builds the one-line rejection message for a malformed `vulns`
// list: `invalid vulns frontmatter: field=vulns value=<raw> — <reason>`. The
// raw value is rendered with `%v` so the operator correcting the task sees the
// value that failed, including a scalar, a mapping, or a list with a bad
// element.
func invalidVulns(ctx context.Context, raw interface{}, reason string) error {
	return errors.Errorf(
		ctx,
		"invalid vulns frontmatter: field=vulns value=%v — %s",
		raw,
		reason,
	)
}

// vulnScopeSection renders the `## Vuln Scope` context section appended to the
// planning prompt when the task carries a non-empty `vulns` list. It tells the
// model that the listed advisories are the whole scope, so stale
// non-vulnerable dependencies do not count as work. Returns "" for a task that
// is not vuln-scoped, so the non-vuln prompt is byte-for-byte unchanged.
func vulnScopeSection(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return "## Vuln Scope\n\n" +
		"This task is vuln-scoped: its `vulns` list names " + strings.Join(ids, ", ") +
		". Those advisories are the WHOLE scope. Stale non-vulnerable dependencies " +
		"are out of scope — `dep_updates_expected` must NOT contribute to has_work. " +
		"Report a fix-action finding only for a listed advisory the repo's gate " +
		"output still reports, plus the go-directive bump when the repo's directive " +
		"is behind."
}
