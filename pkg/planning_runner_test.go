// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	claudelib "github.com/bborbe/agent/claude"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pkg "github.com/bborbe/github-update-go-agent/pkg"
)

var _ = Describe("PlanningRunner", func() {
	Describe("extractToolResultBodies", func() {
		It("captures a tool_result body from a message.content line verbatim", func() {
			line := []byte(
				`{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"content":[{"type":"text","text":"GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6"}]}]}}`,
			)
			Expect(
				pkg.ExtractToolResultBodies(line),
			).To(Equal([]string{"GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6"}))
		})

		It("captures a string-content tool_result body verbatim (the blocked-claim shape)", func() {
			line := []byte(
				`{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"content":"make: cannot access workdir /tmp/github-update-go-x"}]}}`,
			)
			Expect(
				pkg.ExtractToolResultBodies(line),
			).To(Equal([]string{"make: cannot access workdir /tmp/github-update-go-x"}))
		})

		It("captures both the direct text and the nested string content in order", func() {
			line := []byte(
				`{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"text":"direct","content":"nested"}]}}`,
			)
			Expect(pkg.ExtractToolResultBodies(line)).To(Equal([]string{"direct", "nested"}))
		})

		It("returns nil for a tool_use line", func() {
			line := []byte(
				`{"type":"message","message":{"content":[{"type":"tool_use","id":"toolu_01abc","name":"Read","input":{}}]}}`,
			)
			Expect(pkg.ExtractToolResultBodies(line)).To(BeNil())
		})

		It("returns nil for a result line", func() {
			line := []byte(`{"type":"result","result":"{\"outcome\":\"ready\"}"}`)
			Expect(pkg.ExtractToolResultBodies(line)).To(BeNil())
		})

		It("returns nil for malformed JSON", func() {
			Expect(pkg.ExtractToolResultBodies([]byte("not json"))).To(BeNil())
		})
	})

	Describe("planningResultText", func() {
		It("returns the result event text for a non-empty result line", func() {
			text, ok := pkg.PlanningResultText(
				[]byte(`{"type":"result","result":"{\"outcome\":\"ready\"}"}`),
			)
			Expect(ok).To(BeTrue())
			Expect(text).To(Equal(`{"outcome":"ready"}`))
		})

		It("returns false for a non-result line", func() {
			_, ok := pkg.PlanningResultText([]byte(`{"type":"message","message":{"content":[]}}`))
			Expect(ok).To(BeFalse())
		})

		It("returns false for an empty result", func() {
			_, ok := pkg.PlanningResultText([]byte(`{"type":"result","result":""}`))
			Expect(ok).To(BeFalse())
		})

		It("returns false for malformed JSON", func() {
			_, ok := pkg.PlanningResultText([]byte("not json"))
			Expect(ok).To(BeFalse())
		})
	})

	Describe("scanPlanningOutput", func() {
		It("captures the tool_result body verbatim and returns the result text", func() {
			stream := "{\"type\":\"system\",\"subtype\":\"init\"}\n" +
				"{\"type\":\"message\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"toolu_01abc\",\"is_error\":false,\"content\":[{\"type\":\"text\",\"text\":\"GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6\"}]}]}}\n" +
				"{\"type\":\"result\",\"result\":\"{\\\"outcome\\\":\\\"ready\\\"}\"}\n"
			captured := []string{}
			sink := func(_ context.Context, body string) { captured = append(captured, body) }
			resultText, _ := pkg.ScanPlanningOutput(
				context.Background(),
				strings.NewReader(stream),
				sink,
			)
			Expect(captured).To(Equal([]string{"GO-2026-5026 | stdlib | 1.26.5 | fixed 1.26.6"}))
			Expect(resultText).To(Equal(`{"outcome":"ready"}`))
		})

		It("redacts token material at the sink boundary", func() {
			stream := "{\"type\":\"message\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"toolu_01abc\",\"is_error\":false,\"content\":[{\"type\":\"text\",\"text\":\"clone failed: https://x-access-token:ghs_secret123@github.com/o/r.git\"}]}]}}\n"
			captured := []string{}
			sink := func(_ context.Context, body string) { captured = append(captured, body) }
			_, _ = pkg.ScanPlanningOutput(context.Background(), strings.NewReader(stream), sink)
			Expect(
				captured,
			).To(Equal([]string{"clone failed: https://x-access-token:[REDACTED]@github.com/o/r.git"}))
			Expect(strings.Join(captured, "\n")).NotTo(ContainSubstring("ghs_secret123"))
		})

		It("honours context cancellation", func() {
			stream := "{\"type\":\"message\",\"message\":{\"content\":[{\"type\":\"tool_result\",\"tool_use_id\":\"toolu_01abc\",\"is_error\":false,\"content\":[{\"type\":\"text\",\"text\":\"body\"}]}]}}\n"
			captured := []string{}
			sink := func(_ context.Context, body string) { captured = append(captured, body) }
			cancelCtx, cancel := context.WithCancel(context.Background())
			cancel()
			resultText, tail := pkg.ScanPlanningOutput(cancelCtx, strings.NewReader(stream), sink)
			Expect(resultText).To(Equal(""))
			Expect(tail).To(BeNil())
			Expect(captured).To(BeEmpty())
		})

		It("bounds the failure tail to 5 lines, truncating each at 512 bytes", func() {
			longLine := strings.Repeat("x", 600)
			stream := strings.Repeat(
				"{\"type\":\"system\",\"subtype\":\"init\"}\n",
				8,
			) + longLine + "\n"
			_, tail := pkg.ScanPlanningOutput(
				context.Background(),
				strings.NewReader(stream),
				func(_ context.Context, _ string) {},
			)
			Expect(tail).To(HaveLen(5))
			Expect(tail[len(tail)-1]).To(Equal(longLine[:512]))
		})

		It("skips empty lines in the failure tail", func() {
			stream := "line1\n\nline2\n"
			_, tail := pkg.ScanPlanningOutput(
				context.Background(),
				strings.NewReader(stream),
				func(_ context.Context, _ string) {},
			)
			Expect(tail).To(Equal([]string{"line1", "line2"}))
		})
	})

	Describe("RefuteEnvironmentClaim", func() {
		It("refutes an environment claim when the workdir exists on disk", func() {
			dir, err := os.MkdirTemp("", "github-update-go-refute")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, dir)
			Expect(
				pkg.RefuteEnvironmentClaim(dir, "cannot access workdir — blocked by sandbox"),
			).To(BeTrue())
		})

		It("stands when the workdir does not exist", func() {
			base, err := os.MkdirTemp("", "github-update-go-refute")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, base)
			missing := filepath.Join(base, "no-such-workdir")
			Expect(
				pkg.RefuteEnvironmentClaim(missing, "cannot access workdir — blocked by sandbox"),
			).To(BeFalse())
		})

		It("never refutes a non-environment reason regardless of the path", func() {
			dir, err := os.MkdirTemp("", "github-update-go-refute")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, dir)
			Expect(pkg.RefuteEnvironmentClaim(dir, "no fixed version available")).To(BeFalse())
		})
	})

	Describe("Run", func() {
		It("spawns a claude CLI subprocess and parses the stream-json result", func() {
			binDir, err := os.MkdirTemp("", "github-update-go-fake-claude")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, binDir)
			script := filepath.Join(binDir, "claude")
			scriptContent := `#!/bin/sh
printf '%s\n' '{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"content":[{"type":"text","text":"fake tool body"}]}]}}'
printf '%s\n' '{"type":"result","result":"{\"outcome\":\"ready\"}"}'
exit 0
`
			Expect(os.WriteFile(script, []byte(scriptContent), 0o755)).To(Succeed())

			oldPATH := os.Getenv("PATH")
			Expect(os.Setenv("PATH", binDir+":"+oldPATH)).To(Succeed())
			DeferCleanup(os.Setenv, "PATH", oldPATH)

			workDir, err := os.MkdirTemp("", "github-update-go-agent-dir")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, workDir)

			runner := pkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{
				Model:            claudelib.ClaudeModel("sonnet"),
				WorkingDirectory: claudelib.AgentDir(workDir),
			})
			result, err := runner.Run(context.Background(), "the-prompt")
			Expect(err).To(BeNil())
			Expect(result.Result).To(Equal(`{"outcome":"ready"}`))
		})

		It("surfaces the bounded stdout tail when the claude CLI fails", func() {
			binDir, err := os.MkdirTemp("", "github-update-go-fake-claude")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, binDir)
			script := filepath.Join(binDir, "claude")
			scriptContent := `#!/bin/sh
printf '%s\n' '{"type":"message","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_01abc","is_error":false,"content":[{"type":"text","text":"fake failure body"}]}]}}'
printf '%s\n' 'claude crashed'
exit 1
`
			Expect(os.WriteFile(script, []byte(scriptContent), 0o755)).To(Succeed())

			oldPATH := os.Getenv("PATH")
			Expect(os.Setenv("PATH", binDir+":"+oldPATH)).To(Succeed())
			DeferCleanup(os.Setenv, "PATH", oldPATH)

			runner := pkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{})
			_, err = runner.Run(context.Background(), "the-prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("claude CLI failed"))
			Expect(err.Error()).To(ContainSubstring("fake failure body"))
		})

		It("fails with a start error when claude is not on PATH", func() {
			emptyDir, err := os.MkdirTemp("", "github-update-go-empty-path")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, emptyDir)
			oldPATH := os.Getenv("PATH")
			Expect(os.Setenv("PATH", emptyDir)).To(Succeed())
			DeferCleanup(os.Setenv, "PATH", oldPATH)

			runner := pkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{})
			_, err = runner.Run(context.Background(), "the-prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("start claude CLI"))
		})

		It("reports 'no stdout captured' when the claude CLI fails with no output", func() {
			binDir, err := os.MkdirTemp("", "github-update-go-fake-claude")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, binDir)
			script := filepath.Join(binDir, "claude")
			Expect(os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755)).To(Succeed())

			oldPATH := os.Getenv("PATH")
			Expect(os.Setenv("PATH", binDir+":"+oldPATH)).To(Succeed())
			DeferCleanup(os.Setenv, "PATH", oldPATH)

			runner := pkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{})
			_, err = runner.Run(context.Background(), "the-prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("claude CLI failed"))
			Expect(err.Error()).To(ContainSubstring("no stdout captured"))
		})

		It("fails when no result event appears in the output", func() {
			binDir, err := os.MkdirTemp("", "github-update-go-fake-claude")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, binDir)
			script := filepath.Join(binDir, "claude")
			scriptContent := "#!/bin/sh\nprintf '%s\\n' 'nothing useful'\nexit 0\n"
			Expect(os.WriteFile(script, []byte(scriptContent), 0o755)).To(Succeed())

			oldPATH := os.Getenv("PATH")
			Expect(os.Setenv("PATH", binDir+":"+oldPATH)).To(Succeed())
			DeferCleanup(os.Setenv, "PATH", oldPATH)

			runner := pkg.NewPlanningRunner(claudelib.ClaudeRunnerConfig{})
			_, err = runner.Run(context.Background(), "the-prompt")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no result event found in claude CLI output"))
		})
	})

	Describe("subprocess env boundary", func() {
		It("never leaks non-allowlisted pod secrets into the Claude CLI env", func() {
			Expect(os.Setenv("SOME_POD_SECRET", "xyz")).To(Succeed())
			DeferCleanup(os.Unsetenv, "SOME_POD_SECRET")

			cfgDir, err := os.MkdirTemp("", "github-update-go-claude-config")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, cfgDir)
			Expect(os.Setenv("CLAUDE_CONFIG_DIR", cfgDir)).To(Succeed())
			DeferCleanup(os.Unsetenv, "CLAUDE_CONFIG_DIR")

			workDir, err := os.MkdirTemp("", "github-update-go-agent-dir")
			Expect(err).To(BeNil())
			DeferCleanup(os.RemoveAll, workDir)

			config := claudelib.ClaudeRunnerConfig{
				AllowedTools:     claudelib.AllowedTools{"Read", "Grep"},
				Model:            claudelib.ClaudeModel("sonnet"),
				WorkingDirectory: claudelib.AgentDir(workDir),
				Env:              map[string]string{"GH_TOKEN": "tok123"},
			}
			runner := pkg.PlanningRunnerForTest(config, func(_ context.Context, _ string) {})
			cmd, err := pkg.PlanningRunnerBuildCmd(runner, context.Background(), "prompt")
			Expect(err).To(BeNil())

			Expect(cmd.Args).To(ContainElements(
				"claude", "--print", "--output-format", "stream-json", "--verbose",
				"--strict-mcp-config", "--allowedTools", "--model", "sonnet",
			))
			Expect(cmd.Dir).To(Equal(workDir))

			envJoined := strings.Join(cmd.Env, "\n")
			Expect(envJoined).NotTo(ContainSubstring("SOME_POD_SECRET"))
			Expect(envJoined).To(ContainSubstring("CLAUDE_CONFIG_DIR=" + cfgDir))
			Expect(envJoined).To(ContainSubstring("GH_TOKEN=tok123"))
		})
	})
})
