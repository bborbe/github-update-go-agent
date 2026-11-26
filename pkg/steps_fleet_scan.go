// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentlib "github.com/bborbe/agent"
	task "github.com/bborbe/agent/command/task"
	"github.com/bborbe/errors"
	"github.com/bborbe/maintainer/repoallowlist"
	"github.com/golang/glog"
	"golang.org/x/mod/modfile"

	"github.com/bborbe/github-update-go-agent/pkg/maintainerconfig"
)

// fleetScanSectionHeading is the task section the scan writes its report into.
const fleetScanSectionHeading = "## Fleet Scan"

// repoMeta carries the default-branch facts resolved during the population
// gate. It is kept out of ScanCandidateFacts so the population predicate sees
// only the consent and module-file signals.
type repoMeta struct {
	branch  string
	headSHA string
}

// fleetScanStep implements agentlib.Step for the deterministic, LLM-free
// nightly fleet scan: enumerate the allowlisted population, gate it on
// consent and go.mod presence, clone each survivor, run its gate targets, and
// record the firing (unsuppressed) advisory IDs.
type fleetScanStep struct {
	deps FleetScanDeps
}

// NewFleetScanStep wires the fleet scan step with its GitHub read seams, the
// consent fetcher, the GitOps and GateRunner seams, and the injected clock.
func NewFleetScanStep(deps FleetScanDeps) agentlib.Step {
	return &fleetScanStep{deps: deps}
}

// Name implements agentlib.Step.
func (s *fleetScanStep) Name() string { return "fleet-scan" }

// ShouldRun always returns true — there is no replay guard: a re-triggered
// Job re-scans, and the identifier dedup in the publishing half absorbs the
// duplicates. The scan never reads wall-clock to decide whether to run.
func (s *fleetScanStep) ShouldRun(_ context.Context, _ *agentlib.Markdown) (bool, error) {
	return true, nil
}

// Run executes the scan: list, gate, scan, report. An empty population
// completes with scanned=0; a rate limit fails naming the repo and the count;
// a per-repo clone or gate failure is a skip, never a run failure.
func (s *fleetScanStep) Run(
	ctx context.Context,
	md *agentlib.Markdown,
) (*agentlib.Result, error) {
	if s.deps.Config.Owner == "" {
		return needsInput(
			"fleet-scan: OWNER is empty — config fault, not a clean fleet",
		), nil
	}
	repos, err := s.deps.Lister.ListRepos(ctx, s.deps.Config.Owner)
	if err != nil {
		return failed("fleet-scan: list repositories: " + err.Error()), nil
	}
	facts, meta, failResult := s.buildFacts(ctx, repos)
	if failResult != nil {
		return failResult, nil
	}
	vulnPop, stalePop := selectPopulations(s.deps.Config.Allowlist, facts)
	scanSet := scanUnion(vulnPop, stalePop)
	staleSet := make(map[string]bool, len(stalePop))
	for _, fact := range stalePop {
		staleSet[fact.Repo] = true
	}
	results, scanned, failResult, err := s.scanAll(ctx, scanSet, meta, staleSet)
	if err != nil {
		return nil, err
	}
	if failResult != nil {
		return failResult, nil
	}
	commands := buildScanCommands(s.deps.Config.Stage, results)
	filed, already := 0, 0
	if s.deps.Store == nil {
		glog.V(2).Infof(
			"fleet-scan: publish disabled — %d command(s) not filed",
			len(commands),
		)
	} else {
		var publishResult *agentlib.Result
		filed, already, publishResult = publishCommands(ctx, s.deps.Store, commands)
		if publishResult != nil {
			return publishResult, nil
		}
	}
	report := FleetScanReport{
		ScannedAt:       s.deps.CurrentDateTime.Now().String(),
		Scanned:         scanned,
		Filed:           filed,
		AlreadyRecorded: already,
		Repos:           results,
	}
	if err := s.writeReport(ctx, md, report); err != nil {
		return nil, err
	}
	glog.Infof("fleet-scan: scanned=%d filed=%d already_recorded=%d", scanned, filed, already)
	return &agentlib.Result{Status: agentlib.AgentStatusDone}, nil
}

// publishCommands files each command in order, skipping one whose identifier
// the store already reports present. A lookup or publish failure returns a
// failed Result naming the filed count so far; a non-nil Result is returned by
// Run verbatim.
func publishCommands(
	ctx context.Context,
	store ScanTaskStore,
	commands []task.CreateCommand,
) (filed, already int, res *agentlib.Result) {
	for _, cmd := range commands {
		select {
		case <-ctx.Done():
			return filed, already, failed(fmt.Sprintf(
				"fleet-scan: publish cancelled after filed=%d",
				filed,
			))
		default:
		}
		has, err := store.Has(ctx, cmd.TaskIdentifier)
		if err != nil {
			return filed, already, failed(fmt.Sprintf(
				"fleet-scan: task lookup failed after filed=%d: %v",
				filed,
				err,
			))
		}
		if has {
			already++
			continue
		}
		if err := store.Publish(ctx, cmd); err != nil {
			return filed, already, failed(fmt.Sprintf(
				"fleet-scan: publish failed after filed=%d: %v",
				filed,
				err,
			))
		}
		filed++
	}
	return filed, already, nil
}

// buildFacts gates every repository in the lister's order and returns the
// allowlist-visible facts plus the resolved branch metadata. A rate limit
// short-circuits with a failed Result naming the repo.
func (s *fleetScanStep) buildFacts(
	ctx context.Context,
	repos []string,
) ([]ScanCandidateFacts, map[string]repoMeta, *agentlib.Result) {
	facts := make([]ScanCandidateFacts, 0, len(repos))
	meta := make(map[string]repoMeta, len(repos))
	for _, repo := range repos {
		fact, repoInfo, ok, err := s.gateRepo(ctx, repo)
		if err != nil {
			if stderrors.Is(err, ErrFleetRateLimited) {
				return nil, nil, failed(fmt.Sprintf(
					"fleet-scan: github rate limited at repo=%s scanned=%d",
					repo,
					0,
				))
			}
			return nil, nil, failed("fleet-scan: gate repo " + repo + ": " + err.Error())
		}
		if !ok {
			continue
		}
		facts = append(facts, fact)
		meta[repo] = repoInfo
	}
	return facts, meta, nil
}

// gateRepo decides whether one repository is allowlist-visible and gathers its
// module-file and consent facts. ok is false when the repo is excluded or
// gated out; a rate limit is returned as an error so the run can stop.
func (s *fleetScanStep) gateRepo(
	ctx context.Context,
	repo string,
) (ScanCandidateFacts, repoMeta, bool, error) {
	if !repoallowlist.IsAllowed(s.deps.Config.Allowlist, scanTarget(repo)) {
		return ScanCandidateFacts{}, repoMeta{}, false, nil
	}
	goMod, err := s.deps.Files.FetchFile(ctx, repo, "go.mod")
	if err != nil {
		if stderrors.Is(err, ErrFleetFileNotFound) {
			return ScanCandidateFacts{Repo: repo}, repoMeta{}, false, nil
		}
		if stderrors.Is(err, ErrFleetRateLimited) {
			return ScanCandidateFacts{}, repoMeta{}, false, err
		}
		glog.Warningf("fleet-scan: fetch go.mod repo=%s err=%v", repo, err)
		return ScanCandidateFacts{}, repoMeta{}, false, nil
	}
	fact := ScanCandidateFacts{
		Repo:          repo,
		GoModPresent:  true,
		GoModParsable: modfileParsable(goMod),
	}
	owner, name, ok := strings.Cut(repo, "/")
	if !ok || owner == "" || name == "" {
		glog.Warningf("fleet-scan: malformed repo %q — skipping", repo)
		return ScanCandidateFacts{}, repoMeta{}, false, nil
	}
	authCloneURL := injectToken(
		normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"),
		s.deps.GhToken,
	)
	branch, headSHA, err := s.deps.GitOps.ResolveDefaultBranch(ctx, authCloneURL)
	if err != nil {
		if stderrors.Is(err, ErrFleetRateLimited) {
			return ScanCandidateFacts{}, repoMeta{}, false, err
		}
		glog.Warningf("fleet-scan: resolve default branch repo=%s err=%v", repo, err)
		return ScanCandidateFacts{}, repoMeta{}, false, nil
	}
	cfgBytes, err := s.deps.Consent.Fetch(ctx, owner, name, branch)
	if err != nil {
		if stderrors.Is(err, maintainerconfig.ErrFileNotFound) {
			return fact, repoMeta{branch: branch, headSHA: headSHA}, true, nil
		}
		glog.Warningf("fleet-scan: fetch consent repo=%s err=%v", repo, err)
		return ScanCandidateFacts{}, repoMeta{}, false, nil
	}
	cfg, parseErr := maintainerconfig.Parse(ctx, cfgBytes)
	if parseErr != nil {
		glog.Warningf("fleet-scan: parse consent repo=%s err=%v", repo, parseErr)
		return fact, repoMeta{branch: branch, headSHA: headSHA}, true, nil
	}
	fact.Consent = cfg.GoUpdate.AutoUpdate
	return fact, repoMeta{branch: branch, headSHA: headSHA}, true, nil
}

// scanAll clones and scans each repository in order. A rate limit fails the
// run naming the repo and the scanned count; a per-repo failure is a skip.
func (s *fleetScanStep) scanAll(
	ctx context.Context,
	repos []string,
	meta map[string]repoMeta,
	staleSet map[string]bool,
) ([]ScanRepoResult, int, *agentlib.Result, error) {
	select {
	case <-ctx.Done():
		return nil, 0, nil, errors.Wrap(ctx, ctx.Err(), "fleet scan cancelled")
	default:
	}
	results := make([]ScanRepoResult, 0, len(repos))
	scanned := 0
	for _, repo := range repos {
		select {
		case <-ctx.Done():
			return nil, scanned, nil, errors.Wrap(ctx, ctx.Err(), "fleet scan cancelled")
		default:
		}
		result, ok, err := s.scanRepo(ctx, repo, meta[repo], staleSet[repo])
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, scanned, nil, errors.Wrap(ctx, ctxErr, "fleet scan cancelled")
			}
			if stderrors.Is(err, ErrFleetRateLimited) {
				return nil, scanned, failed(fmt.Sprintf(
					"fleet-scan: github rate limited at repo=%s scanned=%d",
					repo,
					scanned,
				)), nil
			}
			return nil, scanned, failed(
				"fleet-scan: scan repo " + repo + ": " + err.Error(),
			), nil
		}
		if !ok {
			continue
		}
		results = append(results, result)
		scanned++
	}
	return results, scanned, nil, nil
}

// scanRepo clones one repository, runs its gate targets, and returns the
// firing (unsuppressed) advisory IDs. ok is false when the repo is skipped.
func (s *fleetScanStep) scanRepo(
	ctx context.Context,
	repo string,
	meta repoMeta,
	inStalePopulation bool,
) (ScanRepoResult, bool, error) {
	workdir := filepath.Join(
		os.TempDir(),
		workdirPrefix+"scan-"+sanitizePathComponent(repo),
	)
	_ = os.RemoveAll(workdir)
	defer func() {
		if err := os.RemoveAll(workdir); err != nil {
			glog.Warningf("fleet-scan: workdir cleanup failed: path=%s err=%v", workdir, err)
		}
	}()
	authCloneURL := injectToken(
		normalizeCloneURLToHTTPS("git@github.com:"+repo+".git"),
		s.deps.GhToken,
	)
	if err := s.deps.GitOps.CloneAtRef(ctx, authCloneURL, meta.branch, workdir); err != nil {
		if stderrors.Is(err, ErrFleetRateLimited) {
			return ScanRepoResult{}, false, err
		}
		glog.Warningf("fleet-scan: clone repo=%s err=%v", repo, err)
		return ScanRepoResult{}, false, nil
	}
	ids, ok := s.collectFindings(ctx, repo, workdir)
	if !ok {
		return ScanRepoResult{}, false, nil
	}
	suppressed, err := loadSuppressedVulnIDs(ctx, workdir)
	if err != nil {
		glog.Warningf("fleet-scan: load suppressions repo=%s err=%v", repo, err)
		suppressed = map[string]bool{}
	}
	vulnIDs := make([]string, 0, len(ids))
	for id := range ids {
		if suppressed[id] {
			continue
		}
		vulnIDs = append(vulnIDs, id)
	}
	sort.Strings(vulnIDs)
	if len(vulnIDs) == 0 {
		vulnIDs = nil
	}
	staleIDs, err := s.classifyStale(ctx, repo, workdir, suppressed, inStalePopulation)
	if err != nil {
		return ScanRepoResult{}, false, err
	}
	return ScanRepoResult{
		Repo:     repo,
		Ref:      meta.headSHA,
		CloneURL: "git@github.com:" + repo + ".git",
		VulnIDs:  vulnIDs,
		StaleIDs: staleIDs,
	}, true, nil
}

// classifyStale runs the withdrawn-only stale classification over the repo's
// suppressed IDs. It runs only for a repo in the stale population with at least
// one suppression; every other case returns nil without touching OSV. A nil
// classifier (local replay without the seam) is treated as "no stale
// classification" so the vuln half still runs.
func (s *fleetScanStep) classifyStale(
	ctx context.Context,
	repo, workdir string,
	suppressed map[string]bool,
	inStalePopulation bool,
) ([]string, error) {
	if !inStalePopulation {
		return nil, nil
	}
	if s.deps.Classifier == nil {
		glog.V(2).Infof("fleet-scan: no classifier configured repo=%s", repo)
		return nil, nil
	}
	if len(suppressed) == 0 {
		return nil, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, errors.Wrap(ctx, ctxErr, "fleet scan cancelled")
	}
	ids := make([]string, 0, len(suppressed))
	for id := range suppressed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return withdrawnOnly(s.deps.Classifier.Classify(ctx, workdir, ids)), nil
}

// collectFindings runs every detected gate target and unions the advisory IDs
// its output reports. Output is parsed even when the target exits non-zero; a
// target that fails with no output is a gate failure and skips the repo.
func (s *fleetScanStep) collectFindings(
	ctx context.Context,
	repo, workdir string,
) (map[string]bool, bool) {
	targets, err := detectGateTargets(ctx, workdir)
	if err != nil {
		glog.Warningf("fleet-scan: detect gate targets repo=%s err=%v", repo, err)
		return nil, false
	}
	ids := make(map[string]bool)
	for _, target := range targets {
		output, _, runErr := s.deps.Gate.RunTargetFull(ctx, workdir, target)
		for _, finding := range parseScannerOutput(target, output) {
			ids[finding.ID] = true
		}
		if runErr != nil && output == "" {
			glog.Warningf(
				"fleet-scan: gate target unusable repo=%s target=%s err=%v",
				repo, target, runErr,
			)
			return nil, false
		}
	}
	return ids, true
}

// writeReport marshals and applies the ## Fleet Scan section.
func (s *fleetScanStep) writeReport(
	ctx context.Context,
	md *agentlib.Markdown,
	report FleetScanReport,
) error {
	section, err := agentlib.MarshalSectionTyped(ctx, fleetScanSectionHeading, report)
	if err != nil {
		return errors.Wrap(ctx, err, "marshal "+fleetScanSectionHeading+" section")
	}
	md.ReplaceSection(section)
	return nil
}

// scanUnion returns the deduplicated, sorted union of both populations' repos.
func scanUnion(vuln, stale []ScanCandidateFacts) []string {
	seen := make(map[string]bool, len(vuln)+len(stale))
	repos := make([]string, 0, len(vuln)+len(stale))
	for _, pop := range [][]ScanCandidateFacts{vuln, stale} {
		for _, fact := range pop {
			if seen[fact.Repo] {
				continue
			}
			seen[fact.Repo] = true
			repos = append(repos, fact.Repo)
		}
	}
	sort.Strings(repos)
	return repos
}

// modfileParsable reports whether go.mod parses.
func modfileParsable(goMod []byte) bool {
	_, err := modfile.Parse("go.mod", goMod, nil)
	return err == nil
}
