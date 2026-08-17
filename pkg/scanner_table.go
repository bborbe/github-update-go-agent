// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
)

// scannerFindingIDRegexp anchors the row parse on advisory IDs. Each
// alternative requires the full ID shape (GO-<year>-<n>, CVE-<year>-<n>,
// GHSA-<grp>-<grp>-<grp>) so a longer ID like GO-2026-50260 is captured in
// full and never conflated with the prefix GO-2026-5026.
var scannerFindingIDRegexp = regexp.MustCompile(
	`GO-\d{4}-\d+|CVE-\d{4}-\d+|GHSA-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}`,
)

// ScannerFinding is one parsed finding row from a gate target's raw output.
type ScannerFinding struct {
	ID           string
	Package      string
	FixedVersion string // empty when the scanner reports no fix
	Scanner      string // govulncheck | osv-scanner | trivy | <target> fallback
}

// ScannerTable is the parsed ground-truth findings table for a planning run,
// accumulated across every detected gate target.
type ScannerTable []ScannerFinding

// Contains reports whether id appears verbatim in the table (exact match).
func (t ScannerTable) Contains(id string) bool {
	for _, row := range t {
		if row.ID == id {
			return true
		}
	}
	return false
}

// Row returns the first row whose ID equals id exactly.
func (t ScannerTable) Row(id string) (ScannerFinding, bool) {
	for _, row := range t {
		if row.ID == id {
			return row, true
		}
	}
	return ScannerFinding{}, false
}

// knownGateTargets is the deterministic preference order of repo gate
// targets the planning step looks for (design § 4.3 planning).
var knownGateTargets = []string{"precommit", "check", "vulncheck"}

// detectGateTargets reads <workdir>/Makefile (and Makefile.precommit when
// present — the fleet convention keeps VULNCHECK_IGNORE and vulncheck
// there) and returns the known gate targets that are actually defined, in
// knownGateTargets preference order. Returns nil, nil when the Makefile is
// missing or defines none of the known targets — the caller escalates.
func detectGateTargets(ctx context.Context, workdir string) ([]string, error) {
	makefile, err := os.ReadFile(
		filepath.Join(workdir, "Makefile"),
	) // #nosec G304 -- workdir is os.TempDir-rooted; filename is constant
	if err != nil && !os.IsNotExist(err) {
		return nil, errors.Wrapf(ctx, err, "read Makefile: %s", filepath.Join(workdir, "Makefile"))
	}
	precommit, precommitErr := os.ReadFile(
		filepath.Join(workdir, "Makefile.precommit"),
	) // #nosec G304 -- workdir is os.TempDir-rooted; filename is constant
	if precommitErr != nil && !os.IsNotExist(precommitErr) {
		return nil, errors.Wrapf(
			ctx,
			precommitErr,
			"read Makefile: %s",
			filepath.Join(workdir, "Makefile.precommit"),
		)
	}
	if err != nil && precommitErr != nil {
		// Neither file exists — no gate targets to find.
		return nil, nil
	}

	var defined []string
	for _, name := range knownGateTargets {
		if makefileTargetDefined(string(makefile), name) ||
			makefileTargetDefined(string(precommit), name) {
			defined = append(defined, name)
		}
	}
	return defined, nil
}

// makefileTargetDefined reports whether name is a Makefile target definition
// in content: a line starting at column 0 with `name\s*:`. A bare `name:`
// inside a recipe line (leading tab) does NOT count.
func makefileTargetDefined(content, name string) bool {
	if content == "" {
		return false
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "\t") {
			continue
		}
		if !strings.HasPrefix(line, name) {
			continue
		}
		rest := strings.TrimLeft(line[len(name):], " \t")
		if strings.HasPrefix(rest, ":") {
			return true
		}
	}
	return false
}

// parseScannerOutput parses one gate target's captured raw output into
// finding rows. One row per line that carries an advisory ID; lines without
// an ID are skipped. The three documented scanner shapes are recognized;
// any other ID-bearing line yields a row with only the ID and a
// target-derived scanner, so no finding is ever lost from the ground truth.
func parseScannerOutput(target string, raw string) []ScannerFinding {
	var findings []ScannerFinding
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id := scannerFindingIDRegexp.FindString(line)
		if id == "" {
			continue
		}
		findings = append(findings, scannerFindingForLine(target, line, id))
	}
	return findings
}

// scannerFindingForLine builds one finding row from an ID-bearing scanner
// output line, choosing the shape by the separators present.
func scannerFindingForLine(target, line, id string) ScannerFinding {
	// osv-scanner shape: `GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6`.
	if strings.Contains(line, " | ") && strings.Contains(line, "fixed") {
		return parseOSVScannerLine(line, id)
	}
	// govulncheck shape: `<id>\t<module>@<version> -> <fixed>\t<summary>`.
	if strings.Contains(line, " -> ") && strings.Contains(line, "@") {
		return parseGovulncheckLine(line, id)
	}
	// trivy shape: box-drawing `│` cells with the standard
	// `Library | Vulnerability | Severity | Installed | Fixed | Title` layout.
	if strings.Contains(line, "│") {
		return parseTrivyLine(line, id)
	}
	// fallback — unknown shape, keep the ID and attribute the target's scanner.
	return ScannerFinding{ID: id, Scanner: scannerForTarget(target)}
}

// parseOSVScannerLine parses the osv-scanner row shape
// `GO-2026-1234 | stdlib | 1.26.5 | fixed 1.26.6`.
func parseOSVScannerLine(line, id string) ScannerFinding {
	f := ScannerFinding{ID: id, Scanner: "osv-scanner"}
	fields := strings.Split(line, "|")
	if len(fields) > 1 {
		f.Package = strings.TrimSpace(fields[1])
	}
	last := fields[len(fields)-1]
	if idx := strings.Index(last, "fixed"); idx >= 0 {
		rest := strings.TrimSpace(last[idx+len("fixed"):])
		if tokens := strings.Fields(rest); len(tokens) > 0 {
			f.FixedVersion = tokens[0]
		}
	}
	return f
}

// parseGovulncheckLine parses the govulncheck row shape
// `<id>\t<module>@<version> -> <fixed>\t<summary>`.
func parseGovulncheckLine(line, id string) ScannerFinding {
	f := ScannerFinding{ID: id, Scanner: "govulncheck"}
	arrowIdx := strings.Index(line, " -> ")
	preArrow := strings.TrimSpace(line[:arrowIdx])
	if at := strings.Index(preArrow, "@"); at >= 0 {
		module := preArrow[:at]
		if sp := strings.LastIndexAny(module, " \t"); sp >= 0 {
			module = module[sp+1:]
		}
		f.Package = strings.TrimSpace(module)
	} else {
		f.Package = preArrow
	}
	afterArrow := strings.TrimSpace(line[arrowIdx+len(" -> "):])
	if tokens := strings.Fields(afterArrow); len(tokens) > 0 {
		f.FixedVersion = tokens[0]
	}
	return f
}

// parseTrivyLine parses the trivy row shape with box-drawing `│` cells in
// the standard `Library | Vulnerability | Severity | Installed | Fixed |
// Title` layout.
func parseTrivyLine(line, id string) ScannerFinding {
	f := ScannerFinding{ID: id, Scanner: "trivy"}
	cells := strings.Split(line, "│")
	for i, cell := range cells {
		if strings.TrimSpace(cell) == id {
			if i > 0 {
				f.Package = strings.TrimSpace(cells[i-1])
			}
			if i+3 < len(cells) {
				f.FixedVersion = strings.TrimSpace(cells[i+3])
			}
			break
		}
	}
	return f
}

// scannerForTarget maps a gate target name to the scanner its output
// usually carries, used only as the fallback attribution.
func scannerForTarget(target string) string {
	switch target {
	case "vulncheck":
		return "govulncheck"
	case "osv-scanner":
		return "osv-scanner"
	case "trivy":
		return "trivy"
	default:
		return target
	}
}

// validatePlanAgainstTable returns an error naming the first plan vuln ID
// that does not appear verbatim in the captured scanner table. A plan whose
// IDs are all present returns nil.
func validatePlanAgainstTable(ctx context.Context, plan *PlanOutput, table ScannerTable) error {
	for _, v := range plan.Vulns {
		if !table.Contains(v.ID) {
			return errors.Errorf(ctx, "vuln id %q not found in captured scanner output", v.ID)
		}
	}
	return nil
}

// renderScannerTable renders the findings table for the planning prompt,
// one row per line: `id | package | fixed_version | scanner`.
func renderScannerTable(table ScannerTable) string {
	var b strings.Builder
	for _, row := range table {
		fmt.Fprintf(&b, "%s | %s | %s | %s\n", row.ID, row.Package, row.FixedVersion, row.Scanner)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
