// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"regexp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// taskLinkLine matches the `Task: <uuid>` line the required task-link check
// parses (the same 8-4-4-4-12 shape .github/workflows/check-task-link.yml
// greps for).
var taskLinkLine = regexp.MustCompile(
	`(?m)^Task: [0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

var _ = Describe("buildPRBody", func() {
	It("emits a Task: line matching the vault task UUID", func() {
		body := buildPRBody(
			"45cc6f18-5262-458a-aa4f-c88a2b34efd9",
			&PlanOutput{GateTargets: []string{"test"}},
			&executionReport{},
			nil,
			nil,
		)
		Expect(taskLinkLine.MatchString(body)).To(BeTrue(), body)
	})

	It("omits the Task: line when the task has no identifier", func() {
		body := buildPRBody(
			"",
			&PlanOutput{GateTargets: []string{"test"}},
			&executionReport{},
			nil,
			nil,
		)
		Expect(body).NotTo(ContainSubstring("Task:"))
	})
})
