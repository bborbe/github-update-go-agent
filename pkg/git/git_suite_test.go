// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package git_test

//go:generate go run -mod=mod github.com/maxbrunsfeld/counterfeiter/v6 -generate

import (
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
	// This suite shells out to real git for every spec (init/clone/commit/
	// push) — ~90s on an idle machine and past 120s under `-cover` or load, so
	// the old 120s cap timed out on a run that was in fact passing. 300s keeps
	// a genuine hang failing while giving a healthy run headroom.
	suiteConfig.Timeout = 300 * time.Second
	RunSpecs(t, "Git Suite", suiteConfig, reporterConfig)
}
