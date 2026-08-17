// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"os/exec"
	"strings"

	claudelib "github.com/bborbe/agent/claude"
	"github.com/bborbe/errors"
	"github.com/golang/glog"

	"github.com/bborbe/github-update-go-agent/pkg/git"
)

// planningRunner is an in-repo claudelib.ClaudeRunner for the planning
// sub-call. The agent-lib runner (claudelib.NewClaudeRunner) does not log
// tool_result bodies at the deployed log level (-v=2), so a model claim
// like "cannot access workdir — blocked by sandbox" could previously not be
// confirmed or refuted from the pod log. This runner mirrors the agent-lib
// subprocess construction (same CLI argv, same env allowlist) and
// additionally logs every tool_result content body at glog V(2),
// token-redacted via git.RedactToken.
type planningRunner struct {
	config  claudelib.ClaudeRunnerConfig
	logSink func(context.Context, string)
}

// NewPlanningRunner constructs the tool-result-logging planning runner.
func NewPlanningRunner(config claudelib.ClaudeRunnerConfig) claudelib.ClaudeRunner {
	return &planningRunner{config: config, logSink: defaultLogPlanningToolResult}
}

// Run mirrors the agent-lib claudeRunner.Run flow (argv, stdout pipe, bounded
// failure tail) and additionally streams every tool_result body through the
// injected log sink.
func (r *planningRunner) Run(ctx context.Context, prompt string) (*claudelib.ClaudeResult, error) {
	cmd, err := r.buildCommand(ctx, prompt)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "build command")
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Wrap(ctx, err, "create stdout pipe")
	}

	if err := cmd.Start(); err != nil {
		return nil, errors.Wrap(ctx, err, "start claude CLI")
	}

	resultText, tail := scanPlanningOutput(ctx, stdoutPipe, r.logSink)

	if err := cmd.Wait(); err != nil {
		var tailMsg string
		if len(tail) > 0 {
			tailMsg = strings.Join(tail, planningTailJoiner)
		} else {
			tailMsg = "no stdout captured"
		}
		glog.V(2).Infof("planning: claude CLI failed result_len=%d err=%v", len(resultText), err)
		return nil, errors.Wrapf(ctx, err, "claude CLI failed: %s", tailMsg)
	}

	if resultText == "" {
		return nil, errors.New(ctx, "no result event found in claude CLI output")
	}
	glog.V(2).Infof("planning: claude CLI succeeded result_len=%d", len(resultText))

	return &claudelib.ClaudeResult{Result: resultText}, nil
}

// buildCommand mirrors the agent-lib claude-runner.go buildCommand EXACTLY
// (fixed argv + config-derived args; this is the security-relevant boundary —
// the subprocess env allowlist must be replicated verbatim so no pod secret
// leaks into the Claude CLI).
func (r *planningRunner) buildCommand(ctx context.Context, prompt string) (*exec.Cmd, error) {
	args := []string{
		"--print",
		"--output-format",
		"stream-json",
		"--verbose",
		"--strict-mcp-config",
	}

	if len(r.config.AllowedTools) > 0 {
		args = append(args, "--allowedTools", r.config.AllowedTools.String())
	}

	if r.config.Model != "" {
		args = append(args, "--model", r.config.Model.String())
	}

	// #nosec G204 -- fixed argv (production sets name=claude); no task input reaches the command line
	cmd := exec.CommandContext(ctx, "claude", args...)
	if r.config.WorkingDirectory != "" {
		workDir, err := r.config.WorkingDirectory.Resolve(ctx)
		if err != nil {
			return nil, errors.Wrap(ctx, err, "resolve WorkingDirectory")
		}
		cmd.Dir = workDir
	}

	cmd.Stdin = bytes.NewBufferString(prompt)

	env, err := r.buildSubprocessEnv(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "build subprocess env")
	}
	cmd.Env = env

	return cmd, nil
}

// buildSubprocessEnv constructs the env var slice for the Claude CLI
// subprocess, replicating the agent-lib claude-runner.go buildSubprocessEnv
// line-for-line. Precedence (later layers override earlier):
//
//  1. Allowlist pass-through of safe parent-process vars (HOME, PATH, ...).
//     The parent process (task executor) typically runs with secrets, Kafka
//     creds, and other sensitive vars in its environment; we do NOT want
//     those flowing into Claude sessions by default. Only well-known,
//     non-sensitive vars pass through automatically.
//  2. CLAUDE_CONFIG_DIR: explicit config > parent process env > default
//     "~/.claude".
//  3. Consumer-provided r.config.Env: arbitrary overrides — highest
//     precedence. To pass additional vars (e.g. GH_TOKEN for gh CLI auth),
//     populate ClaudeRunnerConfig.Env.
func (r *planningRunner) buildSubprocessEnv(ctx context.Context) ([]string, error) {
	env := map[string]string{}

	// Layer 1: allowlist pass-through.
	for _, k := range []string{"HOME", "PATH", "USER", "TZ", "ZONEINFO", "TMPDIR", "LANG", "LC_ALL"} {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if v, ok := os.LookupEnv(k); ok {
			env[k] = v
		}
	}

	// Layer 2: CLAUDE_CONFIG_DIR with precedence config > env > default.
	cfgDir := r.config.ClaudeConfigDir
	if cfgDir == "" {
		if envVal := os.Getenv("CLAUDE_CONFIG_DIR"); envVal != "" {
			cfgDir = claudelib.ClaudeConfigDir(envVal)
		}
	}
	if cfgDir == "" {
		cfgDir = claudelib.ClaudeConfigDir("~/.claude")
	}
	resolved, err := cfgDir.Resolve(ctx)
	if err != nil {
		return nil, errors.Wrap(ctx, err, "resolve ClaudeConfigDir")
	}
	env["CLAUDE_CONFIG_DIR"] = resolved

	// Layer 3: consumer-provided env overrides everything above.
	maps.Copy(env, r.config.Env)

	// Convert to []string for exec.Cmd.
	result := make([]string, 0, len(env))
	for k, v := range env {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		result = append(result, k+"="+v)
	}
	return result, nil
}

// planningTailMaxLines / planningTailMaxBytes bound the failure-message tail
// surfaced on subprocess failure, mirroring the agent-lib scanOutput ring
// buffer.
const (
	planningTailMaxLines = 5
	planningTailMaxBytes = 512
	planningTailJoiner   = " | "
)

// appendPlanningTail appends a non-empty line to the ring buffer, truncating
// to planningTailMaxBytes and evicting the oldest entry when over
// planningTailMaxLines (mirrors agent-lib appendTail).
func appendPlanningTail(tail []string, line []byte) []string {
	if len(line) == 0 {
		return tail
	}
	captured := line
	if len(captured) > planningTailMaxBytes {
		captured = captured[:planningTailMaxBytes]
	}
	tail = append(tail, string(captured))
	if len(tail) > planningTailMaxLines {
		tail = tail[len(tail)-planningTailMaxLines:]
	}
	return tail
}

// scanPlanningOutput reads stream-json lines, logs every tool_result body at
// V(2), and returns the last non-empty result event text plus a bounded tail
// of all non-empty lines for failure messages. The log sink is injected so
// tests can capture the written bodies verbatim.
func scanPlanningOutput(
	ctx context.Context,
	reader io.Reader,
	logSink func(context.Context, string),
) (string, []string) {
	var resultText string
	var tail []string
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return "", nil
		default:
		}

		line := scanner.Bytes()
		tail = appendPlanningTail(tail, line)

		for _, body := range extractToolResultBodies(line) {
			logSink(ctx, git.RedactToken(body))
		}

		if text, ok := planningResultText(line); ok {
			resultText = text
		}
	}
	if err := scanner.Err(); err != nil {
		return "", nil
	}
	return resultText, tail
}

// toolResultBlock is one message.content item in a stream-json line. The
// tool_result body lives in a nested content[].text, or directly in a string
// content field.
type toolResultBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Content json.RawMessage `json:"content"`
}

// extractToolResultBodies returns the raw text bodies of every tool_result
// content block in one stream-json line. Claude Code emits tool_result as a
// message.content item (never as a content_block_delta — the stream carries
// text_delta/input_json_delta/thinking_delta, not tool_result). The body
// lives in a nested content[].text, or directly in a string content field.
// Returns nil for lines without a tool_result.
func extractToolResultBodies(line []byte) []string {
	var event struct {
		Message struct {
			Content []toolResultBlock `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return nil
	}

	bodies := make([]string, 0, len(event.Message.Content))
	for _, c := range event.Message.Content {
		if c.Type != "tool_result" {
			continue
		}
		if c.Text != "" {
			bodies = append(bodies, c.Text)
		}
		if len(c.Content) == 0 {
			continue
		}

		// Content may be a plain JSON string (take it verbatim — that is the
		// "blocked"-claim evidence shape and must NOT be silently dropped) or
		// an array of blocks (take each text item's Text if non-empty).
		var content string
		if err := json.Unmarshal(c.Content, &content); err == nil {
			bodies = append(bodies, content)
			continue
		}

		var blocks []toolResultBlock
		if err := json.Unmarshal(c.Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				bodies = append(bodies, b.Text)
			}
		}
	}
	if len(bodies) == 0 {
		return nil
	}
	return bodies
}

// planningResultText returns the result event's text for a stream-json line
// of type "result" with a non-empty result, and false otherwise.
func planningResultText(line []byte) (string, bool) {
	var event struct {
		Type   string `json:"type"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return "", false
	}
	if event.Type != "result" || event.Result == "" {
		return "", false
	}
	return event.Result, true
}

// defaultLogPlanningToolResult writes one redacted tool_result body at the
// deployed log level (V(2) — the image entrypoint runs /main -v=2).
// Installed as planningRunner.logSink by NewPlanningRunner; tests inject
// their own capture func via the constructor instead of swapping package state.
func defaultLogPlanningToolResult(_ context.Context, body string) {
	glog.V(2).Infof("planning: tool_result %s", body)
}
