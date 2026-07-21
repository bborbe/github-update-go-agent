// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import "context"

// Test-only exports for the external pkg_test package.
var (
	NormalizeCloneURLToHTTPS = normalizeCloneURLToHTTPS
	InjectToken              = injectToken
)

// LLMJSONProbe is the typed shape pkg_test uses to exercise
// parseJSONResponse's three extraction strategies without depending on
// PlanOutput/executionReport internals.
type LLMJSONProbe struct {
	Foo string `json:"foo"`
	Bar int    `json:"bar"`
}

// ParseLLMJSONProbe wraps the unexported generic parseJSONResponse,
// instantiated for LLMJSONProbe, so pkg_test can exercise it directly.
func ParseLLMJSONProbe(ctx context.Context, response string) (*LLMJSONProbe, error) {
	return parseJSONResponse[LLMJSONProbe](ctx, response)
}
