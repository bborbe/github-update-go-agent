// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"os/exec"
	"regexp"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
)

// gateTailMaxBytes bounds the captured make output included in failure
// messages — enough to surface the failing scanner/test lines without
// flooding the task page.
const gateTailMaxBytes = 2000

// gateTargetRegexp validates target names before they reach the make argv.
// Gate targets come from the planning LLM's ## Plan output — this is the
// deterministic guard against argv injection.
var gateTargetRegexp = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

//counterfeiter:generate -o ../mocks/gate_runner.go --fake-name GateRunner . GateRunner

// GateRunner runs one repo gate target (`make -C <workdir> <target>`) and
// reports the exit code plus a bounded output tail. The execution step uses
// it as the deterministic green-gate after the Claude sub-call; the
// ai_review step re-runs the same targets independently.
type GateRunner interface {
	// RunTarget runs `make <target>` in workdir. Returns the bounded
	// combined-output tail, the process exit code (0 on success), and a
	// non-nil error when the target failed or could not be started.
	RunTarget(ctx context.Context, workdir, target string) (tail string, exitCode int, err error)
}

// NewOSExecGateRunner returns a GateRunner shelling out to make. The gate
// inherits the full pod env (os.Environ()): repo Makefiles run scanners via
// `go run tool@version`, which needs GOPROXY/GOPATH/HOME plus GH_TOKEN for
// private module fetches — the design accepts in-pod arbitrary code
// execution for own repos (design § 7.2).
func NewOSExecGateRunner() GateRunner {
	return &osExecGateRunner{}
}

type osExecGateRunner struct{}

func (g *osExecGateRunner) RunTarget(
	ctx context.Context,
	workdir, target string,
) (string, int, error) {
	if !gateTargetRegexp.MatchString(target) {
		return "", -1, errors.Errorf(ctx, "invalid gate target name: %q", target)
	}
	glog.V(2).Infof("gate: running make -C %s %s", workdir, target)
	// #nosec G204 -- binary is hardcoded make; workdir is os.TempDir-rooted; target is regexp-validated
	cmd := exec.CommandContext(ctx, "make", "-C", workdir, target)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	tail := truncateTail(string(out), gateTailMaxBytes)
	if err != nil {
		exitCode := -1
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		glog.V(2).Infof("gate: make %s failed exit=%d", target, exitCode)
		return tail, exitCode, errors.Wrapf(ctx, err, "make %s failed", target)
	}
	glog.V(2).Infof("gate: make %s succeeded", target)
	return tail, 0, nil
}

// truncateTail returns the last maxBytes bytes of s with a truncation marker.
func truncateTail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return "...[truncated]" + s[len(s)-maxBytes:]
}
