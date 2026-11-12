// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	"gopkg.in/yaml.v3"
)

// goReleaseIndexURL is the Go release index. It is a package var so tests can
// point it at a local server.
var goReleaseIndexURL = "https://go.dev/dl/?mode=json&include=all"

// goReleaseIndexClient bounds the release-index fetch: a slow index must not
// stall a pipeline run.
var goReleaseIndexClient = &http.Client{Timeout: 20 * time.Second}

// goDirectiveRegexp matches the `go` directive line of a go.mod file.
var goDirectiveRegexp = regexp.MustCompile(`(?m)^go\s+(\S+)\s*$`)

// resolveTargetGo returns the Go version the update should bump to.
//
// The toolchain a repo's CI installs is the toolchain its vulncheck runs
// against. When CI pins that toolchain from a file — an actions/setup-go step
// carrying go-version-file — the go directive in that file is the version CI
// actually runs, and the bump target is the latest published patch for the
// same minor line, read from the Go release index. Without such a step the
// image toolchain remains the target.
//
// A failed index lookup falls back to the image toolchain rather than failing
// the run: the index is a network read and the pipeline must not depend on it
// being reachable.
func resolveTargetGo(ctx context.Context, workdir string) string {
	baseline, ok := ciGoBaseline(workdir)
	if !ok {
		return targetGoVersion()
	}
	minor, ok := goMinorLine(baseline)
	if !ok {
		glog.V(2).
			Infof("target go: baseline %q carries no minor line, using image toolchain", baseline)
		return targetGoVersion()
	}
	latest, err := latestGoPatch(ctx, minor)
	if err != nil {
		glog.Warningf(
			"target go: release index lookup for %s failed, using image toolchain: %v",
			minor,
			err,
		)
		return targetGoVersion()
	}
	return latest
}

// ciGoBaseline returns the go directive that CI installs its toolchain from,
// when a workflow pins actions/setup-go to a version file.
func ciGoBaseline(workdir string) (string, bool) {
	dir := filepath.Join(workdir, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		versionFile, ok := workflowGoVersionFile(filepath.Join(dir, name))
		if !ok {
			continue
		}
		directive, ok := goDirective(filepath.Join(workdir, versionFile))
		if ok {
			return directive, true
		}
	}
	return "", false
}

// workflowDoc is the slice of a GitHub Actions workflow this package reads.
type workflowDoc struct {
	Jobs map[string]struct {
		Steps []struct {
			Uses string         `yaml:"uses"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// workflowGoVersionFile returns the go-version-file value of the workflow's
// setup-go step, when it carries one.
func workflowGoVersionFile(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var doc workflowDoc
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return "", false
	}
	for _, job := range doc.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/setup-go") {
				continue
			}
			value, ok := step.With["go-version-file"]
			if !ok {
				continue
			}
			if s, ok := value.(string); ok && s != "" {
				return s, true
			}
		}
	}
	return "", false
}

// goDirective returns the version named by a go.mod-style `go` directive.
func goDirective(path string) (string, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	match := goDirectiveRegexp.FindSubmatch(content)
	if match == nil {
		return "", false
	}
	return strings.TrimPrefix(string(match[1]), "go"), true
}

// goMinorLine turns "1.27.3" into "1.27".
func goMinorLine(version string) (string, bool) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return parts[0] + "." + parts[1], true
}

// goRelease is one entry of the Go release index.
type goRelease struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// latestGoPatch returns the highest published stable patch release for a minor
// line, without the "go" prefix (e.g. "1.27.4" for "1.27").
func latestGoPatch(ctx context.Context, minor string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, goReleaseIndexURL, nil)
	if err != nil {
		return "", errors.Wrap(ctx, err, "build release index request")
	}
	resp, err := goReleaseIndexClient.Do(req)
	if err != nil {
		return "", errors.Wrap(ctx, err, "fetch release index")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf(ctx, "release index returned status %d", resp.StatusCode)
	}
	var releases []goRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", errors.Wrap(ctx, err, "decode release index")
	}

	prefix := "go" + minor + "."
	best := ""
	bestPatch := -1
	for _, release := range releases {
		if !release.Stable || !strings.HasPrefix(release.Version, prefix) {
			continue
		}
		patch, err := strconv.Atoi(strings.TrimPrefix(release.Version, prefix))
		if err != nil {
			continue
		}
		if patch > bestPatch {
			bestPatch = patch
			best = strings.TrimPrefix(release.Version, "go")
		}
	}
	if best == "" {
		return "", errors.Errorf(ctx, "no stable release published for go%s", minor)
	}
	return best, nil
}
