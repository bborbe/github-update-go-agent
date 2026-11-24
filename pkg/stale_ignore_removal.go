// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bborbe/errors"
)

// Ignore-surface filenames. Removal is confined to these constants joined to
// the workdir, so no caller-controlled path can escape the repository root.
const (
	trivyIgnoreFile     = ".trivyignore"
	osvScannerFile      = ".osv-scanner.toml"
	vulnCheckMakefile   = "Makefile"
	vulnCheckPrecommit  = "Makefile.precommit"
	suppressionFileMode = 0o644
)

// staleClassifications returns only the classifications whose outcome is
// stale. Legitimate and unresolved verdicts are never candidates for removal.
func staleClassifications(classifications []IgnoreClassification) []IgnoreClassification {
	stale := make([]IgnoreClassification, 0, len(classifications))
	for _, c := range classifications {
		if c.Outcome == IgnoreOutcomeStale {
			stale = append(stale, c)
		}
	}
	return stale
}

// removeStaleIgnores removes every stale ID that still qualifies from the
// three ignore surfaces and returns the IDs actually deleted, sorted. It
// re-checks the local conditions against workdir first, so a verdict computed
// before the update ran is not applied to a module graph that has since moved.
// An empty (or fully non-qualifying) list changes no file byte.
func removeStaleIgnores(
	ctx context.Context,
	workdir string,
	classifications []IgnoreClassification,
) ([]string, error) {
	qualifying := recheckStale(ctx, workdir, staleClassifications(classifications))
	if len(qualifying) == 0 {
		return nil, nil
	}
	staleSet := make(map[string]bool, len(qualifying))
	for _, c := range qualifying {
		staleSet[c.ID] = true
	}

	removed := make(map[string]bool)
	for _, surface := range []func(context.Context, string, map[string]bool, map[string]bool) error{
		removeFromTrivyIgnore,
		removeFromOSVScannerTOML,
		removeFromVulnCheckIgnoreFiles,
	} {
		if err := surface(ctx, workdir, staleSet, removed); err != nil {
			return nil, err
		}
	}

	ids := make([]string, 0, len(removed))
	for id := range removed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// removeFromTrivyIgnore rewrites .trivyignore without the stale IDs.
func removeFromTrivyIgnore(
	ctx context.Context,
	workdir string,
	stale, removed map[string]bool,
) error {
	path := filepath.Join(workdir, trivyIgnoreFile)
	content, ok, err := readOptionalFile(ctx, path)
	if err != nil || !ok {
		return err
	}
	updated, changed := removeTrivyIDs(content, stale, removed)
	if !changed {
		return nil
	}
	return writePreservingMode(path, updated)
}

// removeFromOSVScannerTOML rewrites .osv-scanner.toml without the stale IDs'
// [[IgnoredVulns]] tables.
func removeFromOSVScannerTOML(
	ctx context.Context,
	workdir string,
	stale, removed map[string]bool,
) error {
	path := filepath.Join(workdir, osvScannerFile)
	content, ok, err := readOptionalFile(ctx, path)
	if err != nil || !ok {
		return err
	}
	updated, changed := removeOSVScannerIDs(content, stale, removed)
	if !changed {
		return nil
	}
	return writePreservingMode(path, updated)
}

// removeFromVulnCheckIgnoreFiles rewrites the VULNCHECK_IGNORE value in both
// Makefile and Makefile.precommit. The fleet convention keeps it in either.
func removeFromVulnCheckIgnoreFiles(
	ctx context.Context,
	workdir string,
	stale, removed map[string]bool,
) error {
	for _, name := range []string{vulnCheckMakefile, vulnCheckPrecommit} {
		path := filepath.Join(workdir, name)
		content, ok, err := readOptionalFile(ctx, path)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		updated, changed := removeVulnCheckIDs(content, stale, removed)
		if !changed {
			continue
		}
		if err := writePreservingMode(path, updated); err != nil {
			return err
		}
	}
	return nil
}

// readOptionalFile reads path, treating a missing file as (ok=false, nil).
func readOptionalFile(ctx context.Context, path string) (string, bool, error) {
	// #nosec G304 -- path is a constant filename joined to the caller's workdir.
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errors.Wrapf(ctx, err, "read ignore surface %s", path)
	}
	return string(data), true, nil
}

// writePreservingMode writes content to path, keeping the file's existing
// permission bits when they can be read.
func writePreservingMode(path, content string) error {
	mode := os.FileMode(suppressionFileMode)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	// #nosec G306 -- mode is the file's own existing mode (or 0644).
	return os.WriteFile(path, []byte(content), mode)
}

// removeTrivyIDs removes the stale ID lines from a .trivyignore body. A
// blank-line-separated block whose only remaining content would be comments is
// removed whole, because those comments documented the ID that just went away.
func removeTrivyIDs(content string, stale, removed map[string]bool) (string, bool) {
	trailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	drop := markStaleTrivyLines(lines, stale, removed)

	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !drop[i] {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(lines) {
		return content, false
	}
	result := strings.Join(kept, "\n")
	if trailingNewline && result != "" {
		result += "\n"
	}
	return result, true
}

// markStaleTrivyLines marks every line to remove: the stale ID lines, plus a
// block that would otherwise be left holding only the comments that documented
// the removed ID.
func markStaleTrivyLines(lines []string, stale, removed map[string]bool) []bool {
	drop := make([]bool, len(lines))
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}
		start := i
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			i++
		}
		markTrivyBlock(lines, drop, start, i, stale, removed)
	}
	return drop
}

// markTrivyBlock marks one blank-line-separated block.
func markTrivyBlock(
	lines []string,
	drop []bool,
	start, end int,
	stale, removed map[string]bool,
) {
	blockHasStale := false
	for j := start; j < end; j++ {
		id := extractSuppressedID(lines[j])
		if id == "" || !stale[id] {
			continue
		}
		drop[j] = true
		removed[id] = true
		blockHasStale = true
	}
	if blockHasStale && blockOnlyCommentsRemain(lines, drop, start, end) {
		for j := start; j < end; j++ {
			drop[j] = true
		}
	}
}

// blockOnlyCommentsRemain reports whether every not-dropped line in
// [start,end) is blank or a comment. Such a block no longer carries an entry.
func blockOnlyCommentsRemain(lines []string, drop []bool, start, end int) bool {
	for j := start; j < end; j++ {
		if drop[j] {
			continue
		}
		trimmed := strings.TrimSpace(lines[j])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return false
		}
	}
	return true
}

// removeOSVScannerIDs removes the whole [[IgnoredVulns]] table whose id is
// stale. Removing only the id line would leave an orphan reason and an empty
// table, which is broken TOML.
func removeOSVScannerIDs(content string, stale, removed map[string]bool) (string, bool) {
	trailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	drop := make([]bool, len(lines))

	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) != "[[IgnoredVulns]]" {
			i++
			continue
		}
		start := i
		end := i + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "[[") {
			end++
		}
		id := tableID(lines, start, end)
		if id != "" && stale[id] {
			for j := start; j < end; j++ {
				drop[j] = true
			}
			removed[id] = true
		}
		i = end
	}

	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !drop[i] {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(lines) {
		return content, false
	}
	result := strings.Join(kept, "\n")
	if trailingNewline && result != "" {
		result += "\n"
	}
	return result, true
}

// tableID returns the id declared inside one [[IgnoredVulns]] table, or "".
func tableID(lines []string, start, end int) string {
	for i := start; i < end; i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "id") {
			continue
		}
		if id := extractSuppressedID(lines[i]); id != "" {
			return id
		}
	}
	return ""
}

// removeVulnCheckIDs removes the stale tokens from every VULNCHECK_IGNORE
// assignment, including values continued across `\` lines. The assignment is
// kept even when its value becomes empty, so the gate target still parses.
func removeVulnCheckIDs(content string, stale, removed map[string]bool) (string, bool) {
	trailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	drop := make([]bool, len(lines))
	changed := false

	for i := 0; i < len(lines); i++ {
		if !isVulnCheckIgnoreAssignment(lines[i]) {
			continue
		}
		eq := strings.Index(lines[i], "=")
		updated, lineChanged := filterAssignmentLine(lines[i], eq, stale, removed)
		lines[i] = updated
		changed = changed || lineChanged

		for endsWithContinuation(lines[i]) && i+1 < len(lines) {
			i++
			cont, contChanged, emptied := filterContinuationLine(lines[i], stale, removed)
			changed = changed || contChanged
			if emptied {
				lines[i-1] = stripTrailingContinuation(lines[i-1])
				drop[i] = true
				break
			}
			lines[i] = cont
		}
	}
	if !changed {
		return content, false
	}

	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !drop[i] {
			kept = append(kept, line)
		}
	}
	result := strings.Join(kept, "\n")
	if trailingNewline {
		result += "\n"
	}
	return result, true
}

// isVulnCheckIgnoreAssignment reports whether line assigns VULNCHECK_IGNORE.
// A bare reference like `$(VULNCHECK_IGNORE)` has no `=` and is skipped.
func isVulnCheckIgnoreAssignment(line string) bool {
	return strings.Contains(line, "VULNCHECK_IGNORE") && strings.Contains(line, "=")
}

// filterAssignmentLine removes stale tokens from the value on the assignment
// line, keeping the prefix and any trailing continuation backslash.
func filterAssignmentLine(
	line string,
	eq int,
	stale, removed map[string]bool,
) (string, bool) {
	prefix := line[:eq+1]
	value := line[eq+1:]
	continuation := endsWithContinuation(value)
	kept, changed := filterTokens(strings.Fields(value), stale, removed)
	rebuilt := prefix
	if len(kept) > 0 {
		rebuilt += " " + strings.Join(kept, " ")
	}
	if continuation {
		rebuilt += " \\"
	}
	return rebuilt, changed
}

// filterContinuationLine removes stale tokens from one continued value line.
// emptied means every token was stale, so the caller drops the line and the
// previous line's dangling backslash.
func filterContinuationLine(
	line string,
	stale, removed map[string]bool,
) (updated string, changed, emptied bool) {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	continuation := endsWithContinuation(line)
	kept, changed := filterTokens(strings.Fields(line), stale, removed)
	if len(kept) == 0 {
		return "", changed, true
	}
	rebuilt := indent + strings.Join(kept, " ")
	if continuation {
		rebuilt += " \\"
	}
	return rebuilt, changed, false
}

// filterTokens drops tokens present in stale, recording each dropped ID, and
// strips a token's trailing continuation backslash before matching.
func filterTokens(tokens []string, stale, removed map[string]bool) ([]string, bool) {
	kept := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSuffix(token, "\\")
		if stale[token] {
			removed[token] = true
			continue
		}
		kept = append(kept, token)
	}
	return kept, len(kept) != len(tokens)
}

// endsWithContinuation reports whether line ends with a Makefile `\`
// continuation (ignoring trailing spaces and tabs).
func endsWithContinuation(line string) bool {
	return strings.HasSuffix(strings.TrimRight(line, " \t"), "\\")
}

// stripTrailingContinuation removes a trailing `\` and the spaces before it.
func stripTrailingContinuation(line string) string {
	trimmed := strings.TrimRight(line, " \t")
	trimmed = strings.TrimSuffix(trimmed, "\\")
	return strings.TrimRight(trimmed, " \t")
}
