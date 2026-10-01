// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("prCreateArgs", func() {
	It("draft target includes --draft flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft, "")
		Expect(args).To(ContainElement("--draft"))
	})

	It("ready target omits --draft flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetReady, "")
		Expect(args).NotTo(ContainElement("--draft"))
	})

	It("empty label adds no --label flag", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft, "")
		Expect(args).NotTo(ContainElement("--label"))
	})

	It("non-empty label adds --label with the value", func() {
		args := pkg.PRCreateArgs("base", "head", "title", "body", pkg.PRTargetDraft, "auto-merge")
		Expect(args).To(ContainElement("--label"))
		Expect(args).To(ContainElement("auto-merge"))
	})

	It("both targets start with pr create and include base, head, title, body", func() {
		for _, target := range []pkg.PRTarget{pkg.PRTargetDraft, pkg.PRTargetReady} {
			args := pkg.PRCreateArgs("mybase", "myhead", "mytitle", "mybody", target, "")
			Expect(args[0]).To(Equal("pr"))
			Expect(args[1]).To(Equal("create"))
			Expect(args).To(ContainElement("--base"))
			Expect(args).To(ContainElement("mybase"))
			Expect(args).To(ContainElement("--head"))
			Expect(args).To(ContainElement("myhead"))
			Expect(args).To(ContainElement("--title"))
			Expect(args).To(ContainElement("mytitle"))
			Expect(args).To(ContainElement("--body"))
			Expect(args).To(ContainElement("mybody"))
		}
	})
})

var _ = Describe("isMissingLabelError", func() {
	// Regression: 2026-08-19. AUTO_MERGE_LABEL=auto-merge was configured
	// fleet-wide but no repo defined the label, so every deps-sweep run died
	// at PR creation and the drain stopped producing PRs entirely.
	It("matches gh's missing-label refusal", func() {
		Expect(pkg.IsMissingLabelError(
			"could not add label: 'auto-merge' not found",
		)).To(BeTrue())
	})

	It("does not match an unrelated gh failure", func() {
		Expect(pkg.IsMissingLabelError(
			"pull request create failed: GraphQL: No commits between master and feature/x",
		)).To(BeFalse())
	})

	It("does not match a not-found error that is not about a label", func() {
		Expect(pkg.IsMissingLabelError(
			"HTTP 404: Not Found (https://api.github.com/repos/bborbe/nope)",
		)).To(BeFalse())
	})

	It("does not match empty output", func() {
		Expect(pkg.IsMissingLabelError("")).To(BeFalse())
	})
})

var _ = Describe("truncateToLines", func() {
	// buildLog returns a log of n distinct lines plus the failure text; the line
	// at index failAt (0-based) carries the failure, as `gh run view --log-failed`
	// places it near the tail of the log.
	buildLog := func(n, failAt int) (string, string) {
		lines := make([]string, n)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i)
		}
		failure := "FAIL: 69 folded bullet(s) across 205 released section(s) at v0.66.1."
		if failAt >= 0 && failAt < n {
			lines[failAt] = failure
		}
		return strings.Join(lines, "\n"), failure
	}

	It("keeps the tail, so a failure near the end survives the bound", func() {
		// AC1 regression lock: 649 lines, failure at index 618; at n=200 only the
		// final 200 lines survive, and index 618 is one of them.
		in, failure := buildLog(649, 618)
		Expect(pkg.TruncateToLines(in, 200)).To(ContainSubstring(failure))
	})

	It("names the dropped count ahead of the retained tail", func() {
		// AC2: 649 lines at n=200 drops 449.
		in, _ := buildLog(649, 618)
		Expect(pkg.TruncateToLines(in, 200)).To(ContainSubstring("449"))
	})

	It("returns a log already within the bound unchanged and without a marker", func() {
		// AC3: pre-trimmed input, so the helper's TrimSpace path is identity.
		in, _ := buildLog(50, 49)
		got := pkg.TruncateToLines(in, 200)
		Expect(got).To(Equal(in))
		Expect(got).NotTo(ContainSubstring("dropped"))
	})

	It("stays bounded on a very large log", func() {
		// AC4: 10000 lines at n=200 -> 200 retained lines + 1 marker line.
		in, _ := buildLog(10000, 9999)
		got := pkg.TruncateToLines(in, 200)
		Expect(strings.Count(got, "\n")).To(BeNumerically("<=", 201))
	})

	It("returns an empty log unchanged", func() {
		// Failure mode: empty fetched log -> helper returns empty; the caller's
		// body-evidence fallback (unchanged) handles it.
		Expect(pkg.TruncateToLines("", 200)).To(Equal(""))
	})
})
