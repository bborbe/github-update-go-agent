// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// moduleResolveTimeout bounds the module-graph listing command. The listing is
// a read-only query over an already-resolved graph, so it is far cheaper than
// the bulk-update sequence (bulkUpdateTimeout, 8m) — but it is still hard: a
// command that exceeds it is a resolution failure, reported and never retried.
const moduleResolveTimeout = 2 * time.Minute

// moduleListFormat is the `go list -m -f` template. Two whitespace-separated
// columns, one row per module: the module path and its selected version (empty
// for the main module).
const moduleListFormat = "{{.Path}} {{.Version}}"

// ModuleVersion is one module of a worktree's module graph with its installed
// (MVS-selected) version. Version is empty for the main module.
type ModuleVersion struct {
	Path    string
	Version string
}

//counterfeiter:generate -o ../mocks/module_resolver.go --fake-name ModuleResolver . ModuleResolver

// ModuleResolver lists the modules of a worktree's module graph. It is the
// seam the ai_review step resolves an external advisory's package through:
// the installed version of a package lives in the module that provides it,
// and that module may be an indirect requirement.
type ModuleResolver interface {
	// Modules runs `go list -m -f {{.Path}} {{.Version}} all` in workdir under
	// moduleResolveTimeout and returns the parsed module list. A non-nil error
	// means the graph could not be read — the caller fails closed.
	Modules(ctx context.Context, workdir string) ([]ModuleVersion, error)
}

// NewOSExecModuleResolver constructs the production ModuleResolver.
func NewOSExecModuleResolver() ModuleResolver {
	return &osExecModuleResolver{}
}

type osExecModuleResolver struct{}

// Modules shells out to `go list -m` under a hard timeout. Mirrors
// bulkUpdater.run: log before the call so a run that never returns is
// distinguishable from one never attempted, and report the timeout as the
// wrapped cause rather than a bare command failure.
func (r *osExecModuleResolver) Modules(
	ctx context.Context,
	workdir string,
) ([]ModuleVersion, error) {
	ctx, cancel := context.WithTimeout(ctx, moduleResolveTimeout)
	defer cancel()

	args := []string{"list", "-m", "-f", moduleListFormat, "all"}
	glog.V(1).Infof(
		"event=module_graph_exec_start workdir=%s cmd=%q timeout=%s",
		workdir, strings.Join(args, " "), moduleResolveTimeout,
	)
	// #nosec G204 -- binary is hardcoded go; workdir is os.TempDir-rooted;
	// remaining args are fixed literals.
	cmd := exec.CommandContext(ctx, "go", append([]string{"-C", workdir}, args...)...)
	out, err := cmd.CombinedOutput()
	glog.V(1).Infof(
		"event=module_graph_exec_done workdir=%s cmd=%q err=%v",
		workdir, strings.Join(args, " "), err,
	)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.Wrapf(
				ctx, ctx.Err(), "go list -m all exceeded %s", moduleResolveTimeout,
			)
		}
		return nil, errors.Wrapf(
			ctx, err, "go list -m all in %s: %s",
			workdir, truncateTail(string(out), gateTailMaxBytes),
		)
	}
	return parseModuleList(string(out)), nil
}

// parseModuleList parses `go list -m -f '{{.Path}} {{.Version}}' all` output
// into module rows. One row per non-empty line; the version column is empty
// for the main module, which go list prints as a bare path. A line with no
// fields is skipped. A module the parse misses simply never matches a
// package, and the caller's fail-closed check catches it.
func parseModuleList(output string) []ModuleVersion {
	var modules []ModuleVersion
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		switch len(fields) {
		case 0:
			continue
		case 1:
			modules = append(modules, ModuleVersion{Path: fields[0]})
		default:
			modules = append(modules, ModuleVersion{Path: fields[0], Version: fields[1]})
		}
	}
	return modules
}

// moduleForPackage returns the module whose Path is the longest prefix of
// pkgPath at a path-segment boundary, together with that module's installed
// version. found is false when no module in the graph provides pkgPath — the
// caller fails closed. Reading go.mod text is NOT a substitute: module files
// list module paths, never package paths, so an indirectly-required module's
// subpackage appears in no go.mod.
func moduleForPackage(modules []ModuleVersion, pkgPath string) (ModuleVersion, bool) {
	var best ModuleVersion
	found := false
	for _, m := range modules {
		if m.Path == "" {
			continue
		}
		if pkgPath != m.Path && !strings.HasPrefix(pkgPath, m.Path+"/") {
			continue
		}
		if !found || len(m.Path) > len(best.Path) {
			best = m
			found = true
		}
	}
	return best, found
}
