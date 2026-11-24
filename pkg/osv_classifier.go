// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// classifyBudget caps a whole Classify call. Requests past the budget are
// unresolved and stay in place; planning must never be aborted by OSV.
const classifyBudget = 60 * time.Second

// IgnoreClassifier classifies suppressed advisory IDs against OSV and a
// checked-out repository's module graph. It never returns an error: any
// transport, status, parse or graph failure becomes an unresolved verdict so
// planning is never aborted and no suppression is ever removed on a failure.
//
//counterfeiter:generate -o ../mocks/ignore_classifier.go --fake-name IgnoreClassifier . IgnoreClassifier
type IgnoreClassifier interface {
	Classify(ctx context.Context, workdir string, ids []string) []IgnoreClassification
}

// NewOSVIgnoreClassifier constructs the production classifier over fetcher.
func NewOSVIgnoreClassifier(fetcher OSVFetcher) IgnoreClassifier {
	return &osvIgnoreClassifier{fetcher: fetcher}
}

type osvIgnoreClassifier struct {
	fetcher OSVFetcher
}

// classifyCall holds the per-call state: the module graph, the lazily fetched
// Go index, and a record cache. Keeping it per call (never package-level)
// means one Classify never observes another's index or records.
type classifyCall struct {
	ctx          context.Context
	fetcher      OSVFetcher
	versions     map[string]string
	unresolvable map[string]bool
	goDirective  string
	index        *GoVulnIndex
	indexLoaded  bool
	records      map[string]*OSVVulnerability
}

// Classify returns one verdict per input ID, in input order. A module-graph
// failure makes every ID unresolved: a graph failure must never delete
// anything.
func (c *osvIgnoreClassifier) Classify(
	ctx context.Context,
	workdir string,
	ids []string,
) []IgnoreClassification {
	ctx, cancel := context.WithTimeout(ctx, classifyBudget)
	defer cancel()

	versions, unresolvable, goDirective, err := loadModuleGraph(workdir)
	if err != nil {
		glog.V(2).Infof(
			"event=osv_classify_graph_error err=%v",
			errors.Wrapf(ctx, err, "load module graph"),
		)
		return unresolvedAll(ids, IgnoreReasonTransportError)
	}

	call := &classifyCall{
		ctx:          ctx,
		fetcher:      c.fetcher,
		versions:     versions,
		unresolvable: unresolvable,
		goDirective:  goDirective,
		records:      make(map[string]*OSVVulnerability),
	}
	results := make([]IgnoreClassification, 0, len(ids))
	for i, id := range ids {
		if ctx.Err() != nil {
			return append(results, unresolvedAll(ids[i:], IgnoreReasonBudgetExceeded)...)
		}
		results = append(results, call.classify(id))
	}
	return results
}

// classify resolves one ID. A 404 is retried through the Go index before it is
// called not-found; every other fetch failure is a transport error.
func (c *classifyCall) classify(id string) IgnoreClassification {
	record, err := c.fetchRecord(id)
	if err != nil {
		if !errors.Is(err, ErrAdvisoryNotFound) {
			return unresolvedClassification(id, IgnoreReasonTransportError)
		}
		record = c.resolveAlias(id)
		if record == nil {
			return unresolvedClassification(id, IgnoreReasonNotFound)
		}
	}
	if record == nil {
		return unresolvedClassification(id, IgnoreReasonNotFound)
	}
	result := classifyRecord(record, c.versions, c.unresolvable, c.goDirective)
	// The verdict is for the suppressed ID, not the alias record it resolved
	// to, so execution removes the entry the operator actually wrote.
	result.ID = id
	return result
}

// fetchRecord fetches one advisory, memoized for this call so a candidate the
// alias walk already loaded is not fetched twice.
func (c *classifyCall) fetchRecord(id string) (*OSVVulnerability, error) {
	if record, ok := c.records[id]; ok {
		return record, nil
	}
	record, err := c.fetcher.FetchVuln(c.ctx, id)
	if err != nil {
		return nil, err
	}
	c.records[id] = record
	return record, nil
}

// resolveAlias finds the record a 404'd ID was merged into by scanning the Go
// index for an advisory that lists the queried ID among its aliases. The scan
// is independent of the repository's module graph on purpose: an advisory for
// a package that has left go.mod must still resolve, so the withdrawn verdict
// wins over a package-absent guess. Returns nil when the index cannot be
// fetched or no entry carries the queried ID as an alias; the caller then
// records not-found and keeps the suppression.
func (c *classifyCall) resolveAlias(queriedID string) *OSVVulnerability {
	index := c.loadIndex()
	if index == nil {
		return nil
	}
	for _, entry := range *index {
		if entry.ID == "" || !slices.Contains(entry.Aliases, queriedID) {
			continue
		}
		if c.ctx.Err() != nil {
			return nil
		}
		record, err := c.fetchRecord(entry.ID)
		if err != nil || record == nil {
			continue
		}
		return record
	}
	return nil
}

// loadIndex fetches the Go index lazily and memoizes it for this call only.
func (c *classifyCall) loadIndex() *GoVulnIndex {
	if c.indexLoaded {
		return c.index
	}
	c.indexLoaded = true
	index, err := c.fetcher.FetchGoIndex(c.ctx)
	if err != nil {
		glog.V(2).Infof(
			"event=osv_classify_index_error err=%v",
			errors.Wrapf(c.ctx, err, "fetch go index"),
		)
		return nil
	}
	c.index = index
	return index
}

func unresolvedAll(ids []string, reason string) []IgnoreClassification {
	out := make([]IgnoreClassification, 0, len(ids))
	for _, id := range ids {
		out = append(out, unresolvedClassification(id, reason))
	}
	return out
}

func unresolvedClassification(id, reason string) IgnoreClassification {
	return IgnoreClassification{ID: id, Outcome: IgnoreOutcomeUnresolved, Reason: reason}
}

// loadModuleGraph reads workdir's go.mod into the module graph the classifier
// consults. versions maps every required module path (direct and indirect) to
// its version; a replace with a real version rewrites that map. unresolvable
// marks modules whose effective version cannot be trusted: a replace to a
// local path, or a require version an exclude removes.
func loadModuleGraph(
	workdir string,
) (map[string]string, map[string]bool, string, error) {
	path := filepath.Join(workdir, "go.mod")
	data, err := os.ReadFile(path) // #nosec G304 -- workdir is caller-supplied; filename constant
	if err != nil {
		return nil, nil, "", err
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, nil, "", err
	}

	versions := make(map[string]string, len(f.Require))
	for _, r := range f.Require {
		versions[r.Mod.Path] = r.Mod.Version
	}

	unresolvable := make(map[string]bool)
	for _, r := range f.Replace {
		if r.New.Version == "" {
			unresolvable[r.Old.Path] = true
			continue
		}
		versions[r.Old.Path] = r.New.Version
	}
	for _, e := range f.Exclude {
		if versions[e.Mod.Path] == e.Mod.Version {
			unresolvable[e.Mod.Path] = true
		}
	}

	goDirective := ""
	if f.Go != nil {
		goDirective = f.Go.Version
	}
	return versions, unresolvable, goDirective, nil
}

// classifyRecord maps one advisory record to a verdict. Withdrawn wins over
// everything; a non-Go advisory is legitimate because it cannot be evaluated
// against a Go graph; a stdlib advisory is never package-absent.
func classifyRecord(
	record *OSVVulnerability,
	versions map[string]string,
	unresolvable map[string]bool,
	goDirective string,
) IgnoreClassification {
	result := IgnoreClassification{ID: record.ID}
	if record.Withdrawn != "" {
		result.Outcome = IgnoreOutcomeStale
		result.Reason = IgnoreReasonWithdrawn
		return result
	}
	affected := goAffected(record)
	if len(affected) == 0 {
		result.Outcome = IgnoreOutcomeLegitimate
		result.Reason = IgnoreReasonNotGo
		return result
	}
	if hasStdlib(affected) {
		return classifyStdlib(result, affected, goDirective)
	}
	return classifyModules(result, affected, versions, unresolvable)
}

func goAffected(record *OSVVulnerability) []OSVAffected {
	affected := make([]OSVAffected, 0, len(record.Affected))
	for _, a := range record.Affected {
		if a.Package.Ecosystem == "Go" {
			affected = append(affected, a)
		}
	}
	return affected
}

func hasStdlib(affected []OSVAffected) bool {
	for _, a := range affected {
		if a.Package.Name == "stdlib" {
			return true
		}
	}
	return false
}

// classifyStdlib checks the go directive against the stdlib ranges. A missing
// or unparseable directive, or a record with no evaluable ranges, is
// legitimate: never remove on missing data.
func classifyStdlib(
	result IgnoreClassification,
	affected []OSVAffected,
	goDirective string,
) IgnoreClassification {
	result.Package = "stdlib"
	result.InUseVersion = goDirective
	ranges := stdlibRanges(affected)
	if !isValidVersion(goDirective) || !hasEvaluableRanges(ranges) {
		result.Outcome = IgnoreOutcomeLegitimate
		result.Reason = IgnoreReasonVersionAffected
		return result
	}
	if versionInRanges(goDirective, ranges) {
		result.Outcome = IgnoreOutcomeLegitimate
		result.Reason = IgnoreReasonVersionAffected
		return result
	}
	result.Outcome = IgnoreOutcomeStale
	result.Reason = IgnoreReasonVersionNotAffected
	return result
}

func stdlibRanges(affected []OSVAffected) []OSVRange {
	var ranges []OSVRange
	for _, a := range affected {
		if a.Package.Name == "stdlib" {
			ranges = append(ranges, a.Ranges...)
		}
	}
	return ranges
}

type presentPackage struct {
	pkg          string
	version      string
	unresolvable bool
	affected     bool
}

// classifyModules decides package-absent, unresolvable, affected, or
// version-not-affected for a record whose affected packages are Go modules.
func classifyModules(
	result IgnoreClassification,
	affected []OSVAffected,
	versions map[string]string,
	unresolvable map[string]bool,
) IgnoreClassification {
	present := presentPackages(affected, versions, unresolvable)
	if len(present) == 0 {
		result.Outcome = IgnoreOutcomeStale
		result.Reason = IgnoreReasonPackageAbsent
		result.Package = affected[0].Package.Name
		return result
	}

	resolvableAffected, hasUnresolvable := summarizePresent(present)
	if hasUnresolvable && !resolvableAffected {
		p := firstUnresolvable(present)
		return withVerdict(result, IgnoreOutcomeUnresolved, IgnoreReasonUnresolvableVersion, p)
	}
	if resolvableAffected {
		p := firstAffected(present)
		return withVerdict(result, IgnoreOutcomeLegitimate, IgnoreReasonVersionAffected, p)
	}
	p := firstResolvableOrAny(present)
	return withVerdict(result, IgnoreOutcomeStale, IgnoreReasonVersionNotAffected, p)
}

func withVerdict(
	result IgnoreClassification,
	outcome, reason string,
	p presentPackage,
) IgnoreClassification {
	result.Outcome = outcome
	result.Reason = reason
	result.Package = p.pkg
	result.InUseVersion = p.version
	return result
}

func presentPackages(
	affected []OSVAffected,
	versions map[string]string,
	unresolvable map[string]bool,
) []presentPackage {
	present := make([]presentPackage, 0, len(affected))
	for _, a := range affected {
		module, ok := moduleForPath(versions, a.Package.Name)
		if !ok {
			continue
		}
		version := versions[module]
		present = append(present, presentPackage{
			pkg:          a.Package.Name,
			version:      version,
			unresolvable: unresolvable[module],
			affected:     packageAffected(a.Ranges, version),
		})
	}
	return present
}

// packageAffected reports whether version sits in ranges. A package with no
// evaluable ranges is treated as affected: never remove on missing range data.
func packageAffected(ranges []OSVRange, version string) bool {
	if !hasEvaluableRanges(ranges) {
		return true
	}
	return versionInRanges(version, ranges)
}

func summarizePresent(present []presentPackage) (resolvableAffected, hasUnresolvable bool) {
	for _, p := range present {
		if p.unresolvable {
			hasUnresolvable = true
			continue
		}
		if p.affected {
			resolvableAffected = true
		}
	}
	return resolvableAffected, hasUnresolvable
}

func firstUnresolvable(present []presentPackage) presentPackage {
	for _, p := range present {
		if p.unresolvable {
			return p
		}
	}
	return present[0]
}

func firstAffected(present []presentPackage) presentPackage {
	for _, p := range present {
		if !p.unresolvable && p.affected {
			return p
		}
	}
	return present[0]
}

func firstResolvableOrAny(present []presentPackage) presentPackage {
	for _, p := range present {
		if !p.unresolvable {
			return p
		}
	}
	return present[0]
}

// moduleForPath returns the longest module path that equals pkgPath or is a
// path-segment prefix of it. The segment boundary keeps example.com/foo from
// matching example.com/foobar.
func moduleForPath(versions map[string]string, pkgPath string) (string, bool) {
	best := ""
	found := false
	for module := range versions {
		if module == "" {
			continue
		}
		if pkgPath != module && !strings.HasPrefix(pkgPath, module+"/") {
			continue
		}
		if !found || len(module) > len(best) {
			best, found = module, true
		}
	}
	return best, found
}

func isValidVersion(version string) bool {
	v := normalizeVersion(version)
	return v != "" && semver.IsValid(v)
}

func hasEvaluableRanges(ranges []OSVRange) bool {
	for _, r := range ranges {
		if rangeEvaluable(r) {
			return true
		}
	}
	return false
}

// rangeEvaluable reports whether the range has a supported type and at least
// one introduced event to open an interval.
func rangeEvaluable(r OSVRange) bool {
	if r.Type != "SEMVER" && r.Type != "ECOSYSTEM" {
		return false
	}
	for _, e := range r.Events {
		if e.Introduced != "" {
			return true
		}
	}
	return false
}

// versionInRanges reports whether version falls inside any evaluable range.
// An unparseable version never matches.
func versionInRanges(version string, ranges []OSVRange) bool {
	v := normalizeVersion(version)
	if !semver.IsValid(v) {
		return false
	}
	for _, r := range ranges {
		if !rangeEvaluable(r) {
			continue
		}
		if versionInRange(v, r.Events) {
			return true
		}
	}
	return false
}

// versionInRange walks events in order. introduced opens an interval ("0" is
// the lowest version); fixed closes it as [introduced, fixed); last_affected
// closes it as [introduced, last_affected]; a trailing introduced with no
// close is [introduced, ∞). An unparseable boundary makes the range not match.
func versionInRange(v string, events []OSVEvent) bool {
	w := &rangeWalk{v: v}
	for _, e := range events {
		matched, invalid := w.apply(e)
		if invalid {
			return false
		}
		if matched {
			return true
		}
	}
	return w.open && intervalContains(w.v, w.lower, "", false)
}

// rangeWalk is the mutable interval state while walking one range's events.
type rangeWalk struct {
	v     string
	lower string
	open  bool
}

// apply folds one event into the walk. invalid means an unparseable boundary
// makes the whole range not match.
func (w *rangeWalk) apply(e OSVEvent) (matched, invalid bool) {
	switch {
	case e.Introduced != "":
		return w.applyIntroduced(e.Introduced)
	case e.Fixed != "":
		return w.applyClosed(e.Fixed, false)
	case e.LastAffected != "":
		return w.applyClosed(e.LastAffected, true)
	default:
		return false, false
	}
}

func (w *rangeWalk) applyIntroduced(introduced string) (bool, bool) {
	if w.open && intervalContains(w.v, w.lower, "", false) {
		return true, false
	}
	bound, ok := lowerBound(introduced)
	if !ok {
		return false, true
	}
	w.lower, w.open = bound, true
	return false, false
}

func (w *rangeWalk) applyClosed(upper string, inclusive bool) (bool, bool) {
	if !w.open {
		return false, false
	}
	bound := normalizeVersion(upper)
	if !semver.IsValid(bound) {
		return false, true
	}
	matched := intervalContains(w.v, w.lower, bound, inclusive)
	w.open = false
	return matched, false
}

func lowerBound(introduced string) (string, bool) {
	if introduced == "0" {
		return "", true
	}
	v := normalizeVersion(introduced)
	if !semver.IsValid(v) {
		return "", false
	}
	return v, true
}

func intervalContains(v, lower, upper string, inclusive bool) bool {
	if lower != "" && semver.Compare(v, lower) < 0 {
		return false
	}
	if upper == "" {
		return true
	}
	cmp := semver.Compare(v, upper)
	if inclusive {
		return cmp <= 0
	}
	return cmp < 0
}
