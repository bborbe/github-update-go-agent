// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"regexp"
	"strings"

	agentlib "github.com/bborbe/agent"
	"github.com/bborbe/errors"
	"golang.org/x/mod/semver"
)

// externalScannerPrefix labels a findings-table row that came from the task
// frontmatter instead of from a gate target's output. The surviving label is
// `external:<source>`. A gate target can never produce this prefix:
// gateTargetRegexp (`^[A-Za-z0-9._-]+$`) admits no colon, so the
// scannerForTarget fallback label can never be mistaken for an external row.
const externalScannerPrefix = "external:"

// advisoryIDRegexp is the anchored form of scannerFindingIDRegexp: the
// advisory block's `id` must match one of the same alternatives in full
// (GO-<year>-<n>, CVE-<year>-<n>, GHSA-<grp>-<grp>-<grp>), never as a
// prefix. Deriving it from scannerFindingIDRegexp keeps the accepted ID
// shape in exactly one place.
var advisoryIDRegexp = regexp.MustCompile("^(?:" + scannerFindingIDRegexp.String() + ")$")

// AdvisoryBlock is one validated externally-supplied advisory read from the
// task frontmatter's `advisory` mapping. The block shape is frozen: exactly
// the four keys id, package, fixed_version, source, all required and
// non-empty. Unrecognized extra keys are ignored.
type AdvisoryBlock struct {
	ID           string
	Package      string
	FixedVersion string
	Source       string
}

// advisoryFieldNames are the four frozen keys of the advisory block, in the
// order their validation failures are reported (first failure wins, so the
// message is deterministic).
var advisoryFieldNames = []string{"id", "package", "fixed_version", "source"}

// parseAdvisoryBlock reads the task frontmatter's `advisory` mapping and
// validates it. (nil, nil) means the task carries no advisory block — the
// planning table is then the gate output alone and the review rides the gate
// re-run. A non-nil error means the block is present but malformed; the
// caller decides how to fail (planning escalates needs_input, the review
// fails closed). A partial block is never admitted.
func parseAdvisoryBlock(ctx context.Context, md *agentlib.Markdown) (*AdvisoryBlock, error) {
	raw, ok := md.Frontmatter["advisory"]
	if !ok {
		return nil, nil
	}
	fields, ok := advisoryFields(raw)
	if !ok {
		return nil, invalidAdvisory(
			ctx,
			"advisory",
			raw,
			"must be a single mapping with keys id, package, fixed_version, source",
		)
	}

	values := make(map[string]string, len(advisoryFieldNames))
	for _, name := range advisoryFieldNames {
		value, err := advisoryFieldValue(ctx, fields, name)
		if err != nil {
			return nil, err
		}
		values[name] = value
	}

	return &AdvisoryBlock{
		ID:           values["id"],
		Package:      values["package"],
		FixedVersion: values["fixed_version"],
		Source:       values["source"],
	}, nil
}

// advisoryFields narrows the raw `advisory` frontmatter entry to a
// string-keyed mapping. yaml.v3 decodes a nested mapping into the enclosing
// map's own type whenever that type is a string-keyed map with an
// `interface{}` element (decoder.mapping promotes the enclosing type into its
// stringMapType), so the decoded value is an agentlib.TaskFrontmatter rather
// than a bare map[string]interface{}. Both are accepted so the block's shape
// check never depends on that decoding detail; anything else — a list, a
// scalar, nil — is not the frozen block.
func advisoryFields(raw interface{}) (map[string]interface{}, bool) {
	switch v := raw.(type) {
	case map[string]interface{}:
		return v, true
	case agentlib.TaskFrontmatter:
		return v, true
	default:
		return nil, false
	}
}

// advisoryFieldValue validates one field of the advisory mapping and returns
// its trimmed value. A missing key, a non-string value, and an empty string
// are the same failure class — the block's contract is "required and
// non-empty" — so each is rejected with the field's own requirement sentence
// and the `%v` rendering of the raw map entry (a YAML float 1.2 shows as
// `1.2`, a list as its `%v` form, a missing key as `<nil>`).
func advisoryFieldValue(
	ctx context.Context,
	fields map[string]interface{},
	name string,
) (string, error) {
	raw := fields[name]
	value := ""
	if s, ok := raw.(string); ok {
		value = strings.TrimSpace(s)
	}

	switch name {
	case "id":
		if !advisoryIDRegexp.MatchString(value) {
			return "", invalidAdvisory(
				ctx,
				"id",
				raw,
				"must match the advisory-ID shape (GO-<year>-<n>, CVE-<year>-<n>, GHSA-xxxx-xxxx-xxxx)",
			)
		}
	case "package", "source":
		if value == "" {
			return "", invalidAdvisory(ctx, name, raw, "must be non-empty")
		}
	case "fixed_version":
		if !semver.IsValid(value) {
			return "", invalidAdvisory(ctx, "fixed_version", raw, "must be a Go module version")
		}
	}

	return value, nil
}

// invalidAdvisory builds the one-line rejection message for a malformed
// advisory block: `invalid advisory frontmatter: field=<name> value=<raw> —
// <reason>`. The raw value is rendered with `%v` so a non-string YAML scalar
// (1.2) or a list appears verbatim to the operator correcting the task.
func invalidAdvisory(ctx context.Context, field string, raw interface{}, reason string) error {
	return errors.Errorf(
		ctx,
		"invalid advisory frontmatter: field=%s value=%v — %s",
		field,
		raw,
		reason,
	)
}

// advisoryFinding renders an admitted advisory as one findings-table row.
// The scanner label is `external:<source>`, so the model — and the captured
// table — can tell an externally-supplied advisory from a scanner row.
func advisoryFinding(a *AdvisoryBlock) ScannerFinding {
	return ScannerFinding{
		ID:           a.ID,
		Package:      a.Package,
		FixedVersion: a.FixedVersion,
		Scanner:      externalScannerPrefix + a.Source,
	}
}
