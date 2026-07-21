// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
)

// jsonFenceRegexp extracts a fenced JSON block when the LLM ignores the
// raw-JSON-only instruction and wraps its output anyway.
var jsonFenceRegexp = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")

// parseJSONResponse extracts a typed T from an LLM sub-call's raw text
// response. Every planning/execution Claude (or MiniMax) call routes through
// here — shared so a single fix covers both call sites.
//
// Three extraction strategies are tried in order, mirroring
// github-releaser-agent pkg/prompts.ParseBumpVerdict's approach to the same
// problem:
//  1. Parse the trimmed response as a JSON object directly.
//  2. Strip a ```json (or bare ```) fence and parse the inner block.
//  3. Find the LAST balanced {...} block anywhere in the text and parse it.
//
// First strategy to succeed wins. Strategy 3 exists because prompt hardening
// ("final message must be exactly JSON") reduces but cannot eliminate an LLM
// prefacing its JSON with prose: dev run #2 saw MiniMax end its final
// message with a prose paragraph followed by the correct JSON object on its
// own line, which a naive whole-response json.Unmarshal rejected with
// "invalid character 'T' looking for beginning of value".
//
// Errors are wrapped via github.com/bborbe/errors and always contain the
// literal substring "unmarshal llm json response" so callers can grep
// LLM-parse failures apart from other errors.
func parseJSONResponse[T any](ctx context.Context, response string) (*T, error) {
	trimmed := strings.TrimSpace(response)

	// Strategy 1: parse the trimmed response as a JSON object directly.
	var direct T
	if err := json.Unmarshal([]byte(trimmed), &direct); err == nil {
		return &direct, nil
	}

	// Strategy 2: strip a ```json (or bare ```) fence and parse the inner
	// block, tolerating leading/trailing prose around the fence itself.
	if matches := jsonFenceRegexp.FindStringSubmatch(trimmed); len(matches) >= 2 {
		var fenced T
		if err := json.Unmarshal([]byte(strings.TrimSpace(matches[1])), &fenced); err == nil {
			return &fenced, nil
		}
	}

	// Strategy 3: find the last balanced {...} block anywhere in the text.
	block, ok := lastJSONBlock(trimmed)
	if !ok {
		return nil, errors.Errorf(ctx, "unmarshal llm json response: no JSON object found")
	}
	var fromBlock T
	if err := json.Unmarshal([]byte(block), &fromBlock); err != nil {
		return nil, errors.Wrapf(ctx, err, "unmarshal llm json response: %s", block)
	}
	return &fromBlock, nil
}

// lastJSONBlock returns the last balanced {...} substring in s, or
// "", false if none exists. Mirrors github-releaser-agent
// pkg/prompts.lastJSONBlock — kept private to this package to avoid an
// unwanted cross-repo dependency edge.
func lastJSONBlock(s string) (string, bool) {
	end := strings.LastIndex(s, "}")
	if end < 0 {
		return "", false
	}
	depth := 0
	for i := end; i >= 0; i-- {
		switch s[i] {
		case '}':
			depth++
		case '{':
			depth--
			if depth == 0 {
				return s[i : end+1], true
			}
		}
	}
	return "", false
}
