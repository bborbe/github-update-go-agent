// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

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

var _ = Describe("ghTokenCheckStep rate-limit retry", func() {
	const healthy = `{"resources":{"core":{"limit":5000,"remaining":5000}}}`

	It("retries a rate-limited probe after Retry-After", func() {
		var requests int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if atomic.AddInt32(&requests, 1) == 1 {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(healthy))
		}))
		defer server.Close()

		step := pkg.NewGHTokenCheckStepForTest("token", server.URL)
		result, err := step.Run(context.Background(), nil)
		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusDone))
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(2)))
	})

	It("defers a wait longer than the one-second budget", func() {
		var requests int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			atomic.AddInt32(&requests, 1)
			w.Header().Set("Retry-After", "4")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()

		step := pkg.NewGHTokenCheckStepForTest("token", server.URL)
		start := time.Now()
		result, err := step.Run(context.Background(), nil)
		elapsed := time.Since(start)

		Expect(err).To(BeNil())
		Expect(result.Status).To(Equal(agentlib.AgentStatusFailed))
		Expect(result.Message).To(ContainSubstring("HTTP 429"))
		Expect(result.Message).NotTo(ContainSubstring("context deadline exceeded"))
		Expect(atomic.LoadInt32(&requests)).To(Equal(int32(1)))
		Expect(elapsed).To(BeNumerically("<", 5*time.Second))
	})
})
