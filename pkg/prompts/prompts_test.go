// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package prompts_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/bborbe/github-update-go-agent/pkg/prompts"
)

var _ = Describe("PlanningPrompt", func() {
	It("is non-empty", func() {
		Expect(prompts.PlanningPrompt()).NotTo(BeEmpty())
	})

	It("instructs the repo's own gate detection, never a hardcoded scanner", func() {
		Expect(prompts.PlanningPrompt()).To(ContainSubstring("gate targets"))
		Expect(prompts.PlanningPrompt()).To(ContainSubstring("never hardcode"))
	})

	It("carries the fix-vs-park classification", func() {
		Expect(prompts.PlanningPrompt()).To(ContainSubstring(`"fix"`))
		Expect(prompts.PlanningPrompt()).To(ContainSubstring(`"park"`))
	})

	It("is read-only — forbids any modification", func() {
		Expect(prompts.PlanningPrompt()).To(ContainSubstring("READ-ONLY"))
	})
})

var _ = Describe("ExecutionPrompt", func() {
	It("is non-empty", func() {
		Expect(prompts.ExecutionPrompt()).NotTo(BeEmpty())
	})

	It("forbids git and gh — the Go step owns all git/PR side effects", func() {
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("NO git and NO gh tools"))
	})

	It("forbids workflow edits", func() {
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring(".github/workflows/"))
	})

	It("keeps the CHANGELOG under ## Unreleased and forbids version finalize", func() {
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("## Unreleased"))
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("NEVER create or finalize"))
	})

	It("embeds the repair playbook", func() {
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("Repair playbook"))
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("Double-tidy litmus"))
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("bump the parent"))
	})

	It("forbids suppressions", func() {
		Expect(prompts.ExecutionPrompt()).To(ContainSubstring("Never suppress"))
	})
})
