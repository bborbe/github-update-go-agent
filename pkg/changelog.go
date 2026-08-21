// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	"golang.org/x/mod/modfile"
)

// canonicalChangelogPreamble is the frozen header block the bot-review rule
// `changelog/preamble-frozen` requires at the top of every CHANGELOG.md: the
// `# Changelog` title, the "All notable changes" line, the SemVer link, and
// the MAJOR/MINOR/PATCH bullets. A freshly-created file must carry exactly
// this block before `## Unreleased` — anything less trips the rule and fails
// bot review on every new repo (Defect 5, 75 repos).
const canonicalChangelogPreamble = `# Changelog

All notable changes to this project will be documented in this file.

Please choose versions by [Semantic Versioning](http://semver.org/).

* MAJOR version when you make incompatible API changes,
* MINOR version when you add functionality in a backwards-compatible manner, and
* PATCH version when you make backwards-compatible bug fixes.
`

// conventionalPrefixRegexp matches the `changelog/conventional-prefix-required`
// rule's first-token check over `## Unreleased` bullets: `- <word>:` where the
// word is one of the recognised prefixes (feat/fix/refactor/test/docs/chore/
// perf). The deterministic writer emits exactly `chore:` — the dependency-update
// prefix per the changelog guide.
var conventionalPrefixRegexp = regexp.MustCompile(`^- ([a-z]+:)`)

// changelogResult describes what the deterministic CHANGELOG writer changed.
type changelogResult struct {
	// Created is true when CHANGELOG.md did not exist and was created.
	Created bool
	// Bullet is the exact bullet written under ## Unreleased (empty when the
	// section already carried a conventional-prefix bullet and was left
	// untouched).
	Bullet string
}

// EnsureChangelog guarantees the workdir CHANGELOG.md is bot-review-clean
// after an update, deterministically:
//
//   - fresh file (no CHANGELOG.md): create it with the canonical preamble +
//     `## Unreleased` + a `chore:` bullet naming the modules actually bumped.
//   - existing file: guarantee a `## Unreleased` section directly after the
//     preamble (never at the top of the file), and a `chore:` bullet naming
//     the bumps when the section is missing a conventional-prefix bullet.
//   - the bullet derives from the real go.mod diff (baseGoMod = origin/master,
//     work = the workdir), so it describes only what the update actually
//     changed — never a fabricated entry.
//
// The model's CHANGELOG bullet is best-effort (and absent entirely on the
// salvage path); this Go step is the deterministic verdict, mirroring the
// bulk-update / gate-runner pattern. Defects 2 (vague/no-prefix bullet), 3
// (no bullet → no release on autoRelease repos), 5 (bare preamble → critical
// on every fresh repo) are all structural, so all are satisfiable without a
// model.
func EnsureChangelog(
	ctx context.Context,
	workdir string,
	baseGoMod []byte,
) (*changelogResult, error) {
	path := filepath.Join(workdir, changelogFileName)
	existing, err := os.ReadFile(
		path,
	) // #nosec G304 -- workdir is os.TempDir-rooted (setupWorkdir); filename is a constant
	created := os.IsNotExist(err)
	if err != nil && !created {
		return nil, errors.Wrap(ctx, err, "read CHANGELOG.md")
	}

	bullet, err := changelogBulletFromGoModDiff(ctx, baseGoMod, workdir)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "build CHANGELOG bullet from go.mod diff")
	}

	if created {
		content := canonicalChangelogPreamble + "\n\n## Unreleased\n\n" + bullet + "\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil { // #nosec G703 -- workdir is os.TempDir-rooted (setupWorkdir); filename is a constant
			return nil, errors.Wrap(ctx, err, "write new CHANGELOG.md")
		}
		glog.V(2).Infof("changelog: created CHANGELOG.md (bullet=%q)", bullet)
		return &changelogResult{Created: true, Bullet: bullet}, nil
	}

	updated := ensureUnreleasedBullet(ctx, string(existing), bullet)
	if updated == string(existing) {
		return &changelogResult{}, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil { // #nosec G703 -- workdir is os.TempDir-rooted (setupWorkdir); filename is a constant
		return nil, errors.Wrap(ctx, err, "write CHANGELOG.md")
	}
	glog.V(2).Infof("changelog: ensured ## Unreleased bullet (bullet=%q)", bullet)
	return &changelogResult{Bullet: bullet}, nil
}

// ensureUnreleasedBullet returns content guaranteeing a `## Unreleased`
// section after the preamble (inserting it when absent) that carries at least
// one conventional-prefix bullet (appending `bullet` when none exists).
// Returns input unchanged when the section already satisfies the rule.
func ensureUnreleasedBullet(ctx context.Context, content, bullet string) string {
	lines := strings.SplitAfter(content, "\n") // each element keeps its newline
	heading := -1
	nextHeading := len(lines)
	for i, l := range lines {
		select {
		case <-ctx.Done():
			return content
		default:
		}
		trimmed := strings.TrimSpace(l)
		if heading < 0 && strings.HasPrefix(trimmed, "## Unreleased") {
			heading = i
			continue
		}
		if heading >= 0 && strings.HasPrefix(trimmed, "## ") {
			nextHeading = i
			break
		}
	}

	if heading >= 0 {
		// Section exists. If it already carries a conventional-prefix bullet,
		// leave the whole file untouched — the model wrote a valid entry.
		if hasPrefixedBullet(ctx, strings.Join(lines[heading:nextHeading], "")) {
			return content
		}
		// Insert the bullet at the end of the section, before the next heading
		// (or EOF). Keep the section's trailing blank as its separator.
		out := make([]string, 0, nextHeading+3+len(lines)-nextHeading)
		out = append(out, lines[:nextHeading]...)
		out = append(out, bullet, "\n", "\n")
		out = append(out, lines[nextHeading:]...)
		return strings.Join(out, "")
	}

	// No `## Unreleased` — insert it immediately after the preamble, directly
	// above the first top-level heading (newest version at top). Everything up
	// to that heading is the frozen preamble; never place Unreleased inside it.
	firstHeading := len(lines)
	for i, l := range lines {
		select {
		case <-ctx.Done():
			return content
		default:
		}
		if strings.HasPrefix(strings.TrimSpace(l), "## ") {
			firstHeading = i
			break
		}
	}
	out := make([]string, 0, firstHeading+5+len(lines)-firstHeading)
	out = append(out, lines[:firstHeading]...)
	out = append(out, "## Unreleased\n", "\n", bullet, "\n", "\n")
	out = append(out, lines[firstHeading:]...)
	return strings.Join(out, "")
}

// hasPrefixedBullet reports whether the section body contains a bullet whose
// first token is a conventional prefix (`- chore: ...`, `- fix: ...`, ...).
func hasPrefixedBullet(ctx context.Context, section string) bool {
	for _, line := range strings.Split(section, "\n") {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		if conventionalPrefixRegexp.MatchString(line) {
			return true
		}
	}
	return false
}

// changelogBulletFromGoModDiff derives the `chore:` bullet from the real
// go.mod change: the go directive bump (if any) plus every direct requirement
// whose version moved between the base (origin/master) and the workdir.
// Naming the actual modules satisfies the bot's "be specific" guidance — the
// vague `- chore: update dependencies` bullet the model was told to write is
// itself an anti-pattern in the changelog guide.
func changelogBulletFromGoModDiff(
	ctx context.Context,
	baseGoMod []byte,
	workdir string,
) (string, error) {
	work, err := os.ReadFile(
		filepath.Join(workdir, "go.mod"),
	) // #nosec G304 -- workdir is os.TempDir-rooted (setupWorkdir); filename is a constant
	if err != nil {
		// No go.mod in the workdir — the update changed something else; emit
		// the generic dependency bullet rather than failing the run.
		glog.V(2).Infof("changelog: no go.mod in workdir (%v) — generic bullet", err)
		return "- chore: update go module dependencies", nil
	}
	wf, err := modfile.Parse("go.mod", work, nil)
	if err != nil {
		return "", errors.Wrap(ctx, err, "parse workdir go.mod")
	}

	if len(baseGoMod) == 0 {
		// No base snapshot — fall back to the scope-neutral generic bullet.
		return "- chore: update go module dependencies", nil
	}
	bf, err := modfile.Parse("go.mod", baseGoMod, nil)
	if err != nil {
		return "", errors.Wrap(ctx, err, "parse base go.mod")
	}

	var parts []string
	if bf.Go != nil && wf.Go != nil && bf.Go.Version != wf.Go.Version {
		parts = append(parts, "Go to "+wf.Go.Version)
	}
	bumped, err := diffRequireVersions(ctx, bf, wf)
	if err != nil {
		return "", err
	}
	if len(bumped) > 0 {
		parts = append(parts, strings.Join(bumped, ", "))
	}
	if len(parts) == 0 {
		return "- chore: update go module dependencies", nil
	}
	return "- chore: update " + strings.Join(parts, " and "), nil
}

// diffRequireVersions returns the "path to version" strings for every direct
// requirement whose version moved between the base go.mod (origin/master) and
// the workdir go.mod. Cancellation-aware per the go-context pattern.
func diffRequireVersions(
	ctx context.Context,
	bf, wf *modfile.File,
) ([]string, error) {
	baseVersions := map[string]string{}
	for _, r := range bf.Require {
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx, ctx.Err(), "changelog bullet cancelled")
		default:
		}
		if !r.Indirect {
			baseVersions[r.Mod.Path] = r.Mod.Version
		}
	}
	var bumped []string
	for _, r := range wf.Require {
		select {
		case <-ctx.Done():
			return nil, errors.Wrap(ctx, ctx.Err(), "changelog bullet cancelled")
		default:
		}
		if r.Indirect {
			continue
		}
		if v, ok := baseVersions[r.Mod.Path]; ok && v != r.Mod.Version {
			bumped = append(bumped, r.Mod.Path+" to "+r.Mod.Version)
		}
	}
	sort.Strings(bumped)
	return bumped, nil
}
