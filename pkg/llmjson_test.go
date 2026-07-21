// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("parseJSONResponse", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	DescribeTable("extracts JSON from real-world LLM response shapes",
		func(response string, expected pkg.LLMJSONProbe) {
			got, err := pkg.ParseLLMJSONProbe(ctx, response)
			Expect(err).To(BeNil())
			Expect(*got).To(Equal(expected))
		},
		Entry(
			"pure JSON",
			`{"foo":"hello","bar":42}`,
			pkg.LLMJSONProbe{Foo: "hello", Bar: 42},
		),
		Entry(
			"fenced JSON with json tag",
			"```json\n{\"foo\":\"hello\",\"bar\":42}\n```",
			pkg.LLMJSONProbe{Foo: "hello", Bar: 42},
		),
		Entry(
			"fenced JSON without json tag",
			"```\n{\"foo\":\"hello\",\"bar\":42}\n```",
			pkg.LLMJSONProbe{Foo: "hello", Bar: 42},
		),
		Entry(
			"prose paragraph then JSON on its own line — dev run #2 shape",
			"The update is complete. All dependencies were bumped to their "+
				"latest patch versions and the gate re-ran green.\n"+
				`{"foo":"hello","bar":42}`,
			pkg.LLMJSONProbe{Foo: "hello", Bar: 42},
		),
		Entry(
			"prose with braces (e.g. code sample) before the real trailing JSON",
			"Here is the config I used: `{ \"unused\": true }` while patching.\n\n"+
				"Final result:\n"+
				`{"foo":"hello","bar":42}`,
			pkg.LLMJSONProbe{Foo: "hello", Bar: 42},
		),
	)

	It("returns a wrapped error containing the marker substring on garbage input", func() {
		_, err := pkg.ParseLLMJSONProbe(ctx, "no json anywhere in this response, sorry")
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(ContainSubstring("unmarshal llm json response"))
	})

	It("returns a wrapped error for an unbalanced brace", func() {
		_, err := pkg.ParseLLMJSONProbe(ctx, "prose then a stray { unbalanced")
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(ContainSubstring("unmarshal llm json response"))
	})
})
