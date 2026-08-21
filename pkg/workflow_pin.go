// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/bborbe/errors"
	"gopkg.in/yaml.v3"
)

// HardcodedGoVersionPin is one `.github/workflows/*.yml` `go-version:` finding.
type HardcodedGoVersionPin struct {
	// File is the workflow path relative to the repo root, e.g.
	// ".github/workflows/ci.yml".
	File string
	// Value is the pinned Go version literal, e.g. "1.26.5".
	Value string
}

// PinScanResult distinguishes the two pin shapes.
type PinScanResult struct {
	// PlainPins are single-value `go-version:` pins (hardcoded — the repo's CI
	// toolchain is frozen; a bump desyncs it → escalate).
	PlainPins []HardcodedGoVersionPin
	// MatrixPins are `go-version:` pins inside a strategy matrix (multiple Go
	// versions tested deliberately — NOT a hardcode; no escalation).
	MatrixPins []HardcodedGoVersionPin
}

// HasPlainPin reports whether the scan found any hardcoded single-value pin.
func (r PinScanResult) HasPlainPin() bool { return len(r.PlainPins) > 0 }

// ScanWorkflowGoVersionPins walks `.github/workflows/*.yml|.yaml` in workdir
// and classifies every `go-version:` pin as plain (single value → hardcoded)
// or matrix (inside a strategy.matrix → deliberate multi-version testing).
//
// Pure-Go preflight (no LLM): deterministic, cheap, and the first gate before
// any update work — the planning phase escalates on a plain pin instead of
// opening a PR that fails CI (`go.mod requires go >= X (running go Y;
// GOTOOLCHAIN=local)`), which the agent is architecturally forbidden to fix
// (no Workflows permission; committed-files guard rejects workflow edits).
func ScanWorkflowGoVersionPins(ctx context.Context, workdir string) (PinScanResult, error) {
	var result PinScanResult
	workflowsDir := filepath.Join(workdir, ".github", "workflows")
	entries, err := os.ReadDir(workflowsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil // no workflows dir — nothing to scan
		}
		return result, errors.Wrapf(ctx, err, "read workflows dir %s", workflowsDir)
	}
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return result, errors.Wrapf(ctx, ctx.Err(), "scan workflow pins cancelled")
		default:
		}
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path := filepath.Join(workflowsDir, name)
		content, err := os.ReadFile(path) // #nosec G304 -- workdir is os.TempDir-rooted
		if err != nil {
			return result, errors.Wrapf(ctx, err, "read workflow %s", path)
		}
		plain, matrix, err := classifyGoVersionPins(ctx, content)
		if err != nil {
			return result, errors.Wrapf(ctx, err, "parse workflow %s", path)
		}
		rel := filepath.ToSlash(filepath.Join(".github", "workflows", name))
		for _, v := range plain {
			result.PlainPins = append(result.PlainPins, HardcodedGoVersionPin{File: rel, Value: v})
		}
		for _, v := range matrix {
			result.MatrixPins = append(
				result.MatrixPins,
				HardcodedGoVersionPin{File: rel, Value: v},
			)
		}
	}
	return result, nil
}

// classifyGoVersionPins walks the YAML node tree and returns every
// `go-version:` scalar value split by whether the key lives under a
// `strategy.matrix` (matrix) or anywhere else (plain). YAML anchors/aliases
// are followed by the parser, so matrix entries from shared anchors are
// classified by their structural position, not their literal.
func classifyGoVersionPins(
	ctx context.Context,
	content []byte,
) (plain, matrix []string, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, nil, errors.Wrapf(ctx, err, "unmarshal workflow yaml")
	}
	walkGoVersionPins(&doc, false, &plain, &matrix)
	return plain, matrix, nil
}

// walkGoVersionPins recursively visits the YAML node tree, recording every
// `go-version:` value into plain or matrix per whether it sits under a
// `matrix:` key. Template references (`${{ ... }}`) are dynamic — never pins.
func walkGoVersionPins(n *yaml.Node, underMatrix bool, plain, matrix *[]string) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			keyNode := n.Content[i]
			valNode := n.Content[i+1]
			childMatrix := underMatrix
			if keyNode.Value == "matrix" && keyNode.Kind == yaml.ScalarNode {
				childMatrix = true
			}
			if keyNode.Value == "go-version" {
				recordGoVersionValue(valNode, childMatrix, plain, matrix)
			}
			walkGoVersionPins(valNode, childMatrix, plain, matrix)
		}
	case yaml.SequenceNode:
		for _, item := range n.Content {
			walkGoVersionPins(item, underMatrix, plain, matrix)
		}
	case yaml.DocumentNode:
		walkGoVersionPins(n.Content[0], underMatrix, plain, matrix)
	case yaml.AliasNode:
		walkGoVersionPins(n.Alias, underMatrix, plain, matrix)
	}
}

// recordGoVersionValue classifies a single `go-version:` value node. A
// template reference (`${{ ... }}`, e.g. `${{ matrix.go-version }}`) is a
// dynamic value — never a pin. A scalar is a plain (hardcoded) pin unless it
// sits under a matrix; a sequence (list form) is a deliberate multi-version
// test by construction — always matrix, never a hardcode.
func recordGoVersionValue(val *yaml.Node, underMatrix bool, plain, matrix *[]string) {
	switch val.Kind {
	case yaml.ScalarNode:
		if strings.Contains(val.Value, "${{") {
			return
		}
		if underMatrix {
			*matrix = append(*matrix, val.Value)
		} else {
			*plain = append(*plain, val.Value)
		}
	case yaml.SequenceNode:
		for _, item := range val.Content {
			if item.Kind == yaml.ScalarNode {
				*matrix = append(*matrix, item.Value)
			}
		}
	}
}
