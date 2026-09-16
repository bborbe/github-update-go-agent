// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	agentlib "github.com/bborbe/agent"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

// The controller renders the agent's Message verbatim into the task's
// ## Failure entry. Without the step label a reader learns the condition but
// not which step produced it (BRO-21882).
var _ = Describe("ghTokenCheckStep", func() {
	It("labels an unset token with the step name", func() {
		step := pkg.NewGHTokenCheckStepForTest("", "http://127.0.0.1:1/rate_limit")
		result, err := step.Run(context.Background(), nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusNeedsInput))
		Expect(result.Message).To(HavePrefix("gh_token preflight: "))
		Expect(result.Message).To(ContainSubstring("GH_TOKEN not set"))
	})
})
