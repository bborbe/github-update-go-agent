// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package prompts holds the prompt modules for the github-update-go-agent's
// two domain agents. build-fix planning gets a diagnosis prompt; the spec
// writer and chain emission live in Go (deterministic, no LLM).
package prompts

import "fmt"

// BuildFixDiagnosisPrompt builds the planning-phase diagnosis prompt for a
// build-fix task: given the repo, episode SHA, and failing-workflow/log
// evidence, the LLM classifies the failure into exactly one of the four
// fix verdicts and returns a marshaled FixPlanOutput JSON.
//
// The verdict grammar is enforced deterministically by the Go step (parseFixPlan)
// — the prompt only ever asks the model to pick one value.
func BuildFixDiagnosisPrompt(repo, episodeSHA, logEvidence string) string {
	return fmt.Sprintf(`You are diagnosing a failed CI build for a GitHub Go repository.

Repo: %s
Episode SHA: %s

The build-fix agent will act on exactly one verdict you return. The four
verdicts, and what the agent does with each:

1. no_fix_needed — the build is ALREADY GREEN at HEAD (e.g. a transient
   failure, a Dependabot-internal workflow that is not real CI, or the
   episode recovered). The task closes as success.
2. chain_update — the root cause is a STALE DEPENDENCY or VULNERABILITY:
   a dependency failed to update, go.mod is behind, a vuln has a fix.
   The agent chains to the github-update-go agent, which bumps deps.
3. file_spec — the root cause is a CODE or TEST bug in the repo: a test
   asserts something wrong, a function misbehaves, a compile error caused
   by repo code. The agent files a kind:bug spec for dark-factory to fix.
4. needs_input — the evidence is ambiguous or insufficient: you cannot
   confidently classify the root cause. The agent escalates to a human.

Below is the failing-workflow and log evidence from the task:

%s

Return EXACTLY a JSON object, no prose, no markdown fence, of the form:
{"verdict":"<one of the four values>","reason":"<one-sentence diagnosis naming the evidence you based it on>","failing_workflows":["<workflow names, if known>"],"episode_sha":"%s"}

Rules:
- Pick EXACTLY one verdict. Never invent a new value.
- no_fix_needed requires positive evidence the real CI is green at HEAD.
- chain_update vs file_spec: if the failing step names a Go build/vet/test
  against repo code, prefer file_spec; if it names dependency resolution,
  go.sum drift, or a known vulnerable module, prefer chain_update.
- When in doubt, choose needs_input — escalating a genuine bug is safer
  than mis-routing it.`,
		repo, episodeSHA, logEvidence, episodeSHA)
}
