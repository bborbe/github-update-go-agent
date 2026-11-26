// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"fmt"
	"strings"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
)

// buildScanCommands renders one CreateCommand per (repo, family) that has work.
// A repo carrying both a vuln set and a stale set yields two commands.
func buildScanCommands(stage string, results []ScanRepoResult) []task.CreateCommand {
	commands := make([]task.CreateCommand, 0, len(results)*2)
	for _, result := range results {
		owner, name := splitRepoName(result.Repo)
		if len(result.VulnIDs) > 0 {
			fm := scanFrontmatter(result.Repo, result.Ref, stage)
			fm["vulns"] = result.VulnIDs
			fm["vuln_count"] = len(result.VulnIDs)
			commands = append(commands, task.CreateCommand{
				Title: scanTaskTitle("Update Go", result.Repo, result.Ref),
				TaskIdentifier: agentlib.TaskIdentifier(
					DeriveScanVulnTaskID(owner, name, result.VulnIDs),
				),
				Frontmatter: fm,
				Body:        buildScanVulnBody(result.VulnIDs),
			})
		}
		if len(result.StaleIDs) > 0 {
			commands = append(commands, task.CreateCommand{
				Title: scanTaskTitle(
					"Remove stale vuln ignores",
					result.Repo,
					result.Ref,
				),
				TaskIdentifier: agentlib.TaskIdentifier(
					DeriveScanStaleIgnoreTaskID(
						owner,
						name,
						scanStaleGeneration,
						result.StaleIDs,
					),
				),
				Frontmatter: scanFrontmatter(result.Repo, result.Ref, stage),
				Body:        renderStaleBody(result.StaleIDs),
			})
		}
	}
	return commands
}

// scanFrontmatter builds the watchers' envelope shared by both families.
func scanFrontmatter(repo, ref, stage string) agentlib.TaskFrontmatter {
	fm := agentlib.TaskFrontmatter{
		"task_type": "github-update-go",
		"assignee":  "github-update-go-agent",
		"phase":     "planning",
		"status":    "in_progress",
		"repo":      repo,
		"clone_url": "git@github.com:" + repo + ".git",
		"ref":       ref,
	}
	if stage != "" {
		fm["stage"] = stage
	}
	return fm
}

// scanTaskTitle renders "<prefix> <owner>-<repo> <sha:7>". The dash form is
// required: task.CreateCommand.Validate rejects '/' in a title.
func scanTaskTitle(prefix, repo, ref string) string {
	return prefix + " " + strings.ReplaceAll(repo, "/", "-") + " " + scanShortSHA(ref)
}

// scanShortSHA bounds a ref to its 7-char prefix.
func scanShortSHA(ref string) string {
	if len(ref) <= 7 {
		return ref
	}
	return ref[:7]
}

// splitRepoName splits "owner/name" into its two components. A malformed repo
// yields two empty strings rather than a panic.
func splitRepoName(repo string) (owner, name string) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// buildScanVulnBody renders the operator-readable body of a vulnerability
// task: a header naming the count and every advisory ID on its own bullet.
func buildScanVulnBody(ids []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d advisory ID(s) must be cleared:\n\n", len(ids))
	for _, id := range ids {
		fmt.Fprintf(&b, "- %s\n", id)
	}
	return b.String()
}
