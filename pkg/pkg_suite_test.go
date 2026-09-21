// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

//go:generate go run -mod=mod github.com/maxbrunsfeld/counterfeiter/v6 -generate

import (
	"regexp"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/format"
)

func TestSuite(t *testing.T) {
	time.Local = time.UTC
	format.TruncatedDiff = false
	RegisterFailHandler(Fail)
	suiteConfig, reporterConfig := GinkgoConfiguration()
	suiteConfig.Timeout = 120 * time.Second
	RunSpecs(t, "Pkg Suite", suiteConfig, reporterConfig)
}

// taskIdentifierLine matches the `task_identifier:` line of a fixture's
// frontmatter, whatever value it currently carries.
var taskIdentifierLine = regexp.MustCompile(`(?m)^task_identifier:.*$`)

// uniqueTaskMD makes a fixture's task_identifier unique to the running spec.
//
// setupWorkdir derives the clone workdir from task_identifier and RemoveAlls it
// unconditionally (pkg/steps_planning.go), so a fixture constant shared by many
// specs resolves them all to one /tmp path and they destroy each other's clone.
// Serial execution cannot lose that race; Ginkgo's spec-level parallelism
// (`ginkgo -procs=N`) can, which is what made these specs fail nondeterministically.
// Keying the identifier to the spec's own file:line gives every spec its own
// workdir while keeping the identifier deterministic across runs.
func uniqueTaskMD(md string) string {
	spec := CurrentSpecReport()
	id := regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(
		spec.FileName()+"-"+strconv.Itoa(spec.LineNumber()), "-")
	return taskIdentifierLine.ReplaceAllString(md, "task_identifier: "+id)
}
