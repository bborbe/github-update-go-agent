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

// FilterSuppressed returns a copy of the table without rows whose ID is in
// suppressed. The planning stage must not present operator-approved no-fix
// suppressions to the model as park-worthy findings: the gate targets run
// against those very configs (and pass), yet their echoed output can still
// list the suppressed IDs — captured naively, the task re-parks on a
// suppression the operator already approved (design D4). Nil/empty suppressed
// returns the table unchanged.
func (t ScannerTable) FilterSuppressed(suppressed map[string]bool) ScannerTable {
	if len(suppressed) == 0 {
		return t
	}
	filtered := make(ScannerTable, 0, len(t))
	for _, row := range t {
		if suppressed[row.ID] {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
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
//
// suppressedIDRegexp matches an advisory ID on its own inside a suppression
// surface line — an `id = "GO-2026-4923"` TOML entry, a bare `.trivyignore`
// line, or a `VULNCHECK_IGNORE` value token. Word-boundary anchors keep the
// match zero-width so a separator is never consumed: with `\s`/`"` anchors,
// FindAllStringSubmatch consumed the trailing space of one ID and the next
// ID lost its required leading separator (only the first ID ever matched).
// The boundaries also pin the whole token so a longer ID (GO-2026-50260) is
// never matched by its prefix (GO-2026-5026) — the `\d+` is greedy, so the
// full digit run is captured and the trailing `\b` fails inside a longer ID.
var suppressedIDRegexp = regexp.MustCompile(
	`\b(GO-\d{4}-\d+|CVE-\d{4}-\d+|GHSA-[0-9A-Za-z]{4}-[0-9A-Za-z]{4}-[0-9A-Za-z]{4})\b`,
)

// loadSuppressedVulnIDs reads the repo's three fleet-convention suppression
// surfaces in workdir and returns the set of advisory IDs the operator has
// approved as no-fix (design D4):
//
//   - .osv-scanner.toml — [[IgnoredVulns]] entries (`id = "..."`)
//   - .trivyignore — one advisory ID per line, `#` comments allowed
//   - Makefile / Makefile.precommit — `VULNCHECK_IGNORE ?= GO-... GO-...`
//
// Missing surfaces are not errors — a repo without suppressions returns an
// empty set. A surface that exists but cannot be read is an error.
func loadSuppressedVulnIDs(ctx context.Context, workdir string) (map[string]bool, error) {
	suppressed := make(map[string]bool)

	for _, load := range []func(context.Context, string, map[string]bool) error{
		loadOSVScannerSuppressions,
		loadTrivySuppressions,
		loadVulnCheckIgnoreSuppressions,
	} {
		if err := load(ctx, workdir, suppressed); err != nil {
			return nil, err
		}
	}

	return suppressed, nil
}

// loadOSVScannerSuppressions reads `.osv-scanner.toml` [[IgnoredVulns]] entries
// (`id = "..."` lines). A missing file is not an error.
func loadOSVScannerSuppressions(
	ctx context.Context,
	workdir string,
	suppressed map[string]bool,
) error {
	path := filepath.Join(workdir, ".osv-scanner.toml")
	content, err := os.ReadFile(
		path,
	) // #nosec G304 -- workdir is os.TempDir-rooted; filename is constant
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrapf(ctx, err, "read suppression surface %s", path)
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "id") {
			continue
		}
		if id := extractSuppressedID(line); id != "" {
			suppressed[id] = true
		}
	}
	return nil
}

// loadTrivySuppressions reads `.trivyignore` — one advisory ID per line,
// `#` comments and blank lines allowed. A missing file is not an error.
func loadTrivySuppressions(ctx context.Context, workdir string, suppressed map[string]bool) error {
	path := filepath.Join(workdir, ".trivyignore")
	content, err := os.ReadFile(
		path,
	) // #nosec G304 -- workdir is os.TempDir-rooted; filename is constant
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.Wrapf(ctx, err, "read suppression surface %s", path)
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if id := extractSuppressedID(line); id != "" {
			suppressed[id] = true
		}
	}
	return nil
}

// loadVulnCheckIgnoreSuppressions reads `VULNCHECK_IGNORE ?= ...` from
// Makefile and Makefile.precommit. The value is a space-separated ID list;
// `?=` is the fleet form, `=`/`:=` also accepted. The value may span `\`
// continuation lines (osv-scanner gotchas) — accumulate before extracting,
// so no ID in a continued value is missed. A missing file is not an error.
func loadVulnCheckIgnoreSuppressions(
	ctx context.Context,
	workdir string,
	suppressed map[string]bool,
) error {
	for _, name := range []string{"Makefile", "Makefile.precommit"} {
		mkPath := filepath.Join(workdir, name)
		content, err := os.ReadFile(
			mkPath,
		) // #nosec G304 -- workdir is os.TempDir-rooted; name is constant
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return errors.Wrapf(ctx, err, "read suppression surface %s", mkPath)
		}
		extractVulnCheckIgnore(string(content), suppressed)
	}
	return nil
}

// extractVulnCheckIgnore scans content for `VULNCHECK_IGNORE ...=` lines and
// records every advisory ID in their values. A value may span `\` continuation
// lines — accumulate the continued value before extracting so no ID is missed.
func extractVulnCheckIgnore(content string, suppressed map[string]bool) {
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "VULNCHECK_IGNORE") {
			continue
		}
		// Take the value after the assignment operator; skip a bare
		// reference like `$(VULNCHECK_IGNORE)` (no `=`).
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		value := line[eq+1:]
		for strings.HasSuffix(value, "\\") {
			i++
			if i >= len(lines) {
				break
			}
			value = strings.TrimSuffix(value, "\\") + " " + strings.TrimSpace(lines[i])
		}
		for _, m := range suppressedIDRegexp.FindAllStringSubmatch(value, -1) {
			if m[1] != "" {
				suppressed[m[1]] = true
			}
		}
	}
}

// extractSuppressedID returns the advisory ID found anywhere in line, or ""
// when the line carries none. Used across all three suppression surfaces; a
// line never carries more than one ID in practice (TOML one per entry, trivy
// one per line, VULNCHECK_IGNORE value tokens are whitespace-separated).
func extractSuppressedID(line string) string {
	if m := suppressedIDRegexp.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
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

// trivyStatusValues are the values trivy writes in the Status column that
// some table versions insert between Severity and Installed Version. When
// the cell after Severity is one of these, the Fixed Version cell sits one
// position further right than in the legacy six-column layout.
var trivyStatusValues = map[string]bool{
	"fixed":        true,
	"affected":     true,
	"will_not_fix": true,
	"not_affected": true,
	"end_of_life":  true,
}

// parseTrivyLine parses the trivy row shape with box-drawing `│` cells.
// Two layouts occur in the wild:
//
//	Legacy: Library | Vulnerability | Severity | Installed | Fixed | Title
//	Current: Library | Vulnerability | Severity | Status | Installed | Fixed | Title
//
// The Status cell (`fixed`, `affected`, ...) between Severity and Installed
// shifts Fixed from cells[i+3] to cells[i+4]; a parser that always assumes
// the legacy offset reads the Installed version as the fix, so the model's
// targeted `go get <pkg>@<installed>` is a no-op and the gate stays red
// (real incident: golang.org/x/mod v0.39.0 reported as fixed at v0.39.0
// while trivy required v0.40.0).
func parseTrivyLine(line, id string) ScannerFinding {
	f := ScannerFinding{ID: id, Scanner: "trivy"}
	cells := strings.Split(line, "│")
	for i, cell := range cells {
		if strings.TrimSpace(cell) != id {
			continue
		}
		if i > 0 {
			f.Package = strings.TrimSpace(cells[i-1])
		}
		// The cell after the ID is Severity; the one after that is either
		// Installed (legacy) or Status (current). Detect the current layout
		// by the Status cell's value so both shapes parse.
		fixedOffset := 3
		if i+2 < len(cells) && trivyStatusValues[strings.TrimSpace(cells[i+2])] {
			fixedOffset = 4
		}
		if i+fixedOffset < len(cells) {
			f.FixedVersion = strings.TrimSpace(cells[i+fixedOffset])
		}
		break
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
