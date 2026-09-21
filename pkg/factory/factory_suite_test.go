// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

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
	suiteConfig.Timeout = 60 * time.Second
	RunSpecs(t, "Factory Suite", suiteConfig, reporterConfig)
}

// taskIdentifierLine matches the `task_identifier:` line of a fixture's
// frontmatter, whatever value it currently carries.
var taskIdentifierLine = regexp.MustCompile(`(?m)^task_identifier:.*$`)

// uniqueTaskMD makes a fixture's task_identifier unique to the running spec.
//
// setupWorkdir derives the clone workdir from task_identifier and RemoveAlls it
// unconditionally (pkg/steps_planning.go), so a fixture constant shared by many
// specs resolves them all to one /tmp path and they destroy each other's clone.
// The same constant here is shared with package pkg's fixtures, which run
// concurrently with this package under `go test -p=N`, so the collision crosses
// package boundaries too. Keying the identifier to the spec's own file:line
// gives every spec its own workdir while keeping it deterministic across runs.
func uniqueTaskMD(md string) string {
	spec := CurrentSpecReport()
	id := regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(
		spec.FileName()+"-"+strconv.Itoa(spec.LineNumber()), "-")
	return taskIdentifierLine.ReplaceAllString(md, "task_identifier: "+id)
}
