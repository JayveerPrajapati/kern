// Package jscpd implements a Blueprint service.Check that delegates
// duplication detection to the jscpd binary (Rust rewrite v5, binary name
// `cpd`, https://github.com/kucherenko/jscpd).
//
// The check runs a two-pass triage model (P1.1):
//
//   - Pass 1 (always): the in-house structural duplication check
//     (duplication:advisory) produces advisory candidates with confidence
//     scores. Candidates above BlockCandidateThreshold (>0.90) are
//     block-eligible; the rest are advisory WARN and never block.
//
//   - Pass 2 (when the jscpd binary is available): jscpd scans the mirrored
//     repo. A block-eligible candidate whose file pair is also reported as a
//     jscpd clone is escalated to a duplication:confirmed-block BLOCK finding
//     (two-pass confirmed). Candidates jscpd does NOT confirm stay advisory
//     WARN. jscpd-only clones stay WARN (duplication:jscpd:clone).
//
// When the jscpd binary is unavailable (JSCPD_BINARY unset, no `jscpd`/`cpd`
// on $PATH, no npx), the check degrades to a pure advisory fallback: all
// in-house findings stay WARN (no confirmation is possible, so nothing
// escalates) and a duplication:incumbent-unavailable WARN finding is added so
// the fallback is never silent (tracker T2.1 acceptance).
package jscpd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/duplication"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// minTokens is the clone size floor passed to jscpd (`-k, --min-tokens`).
// The v5 default (50) skips short blocks that the in-house check would flag,
// so we lower it to 20 to keep meaningful short clones visible.
const minTokens = 20

// maxEvidenceFragment caps how much of the duplicated fragment is embedded in
// a finding's evidence (fragments can be large; evidence should stay lean).
const maxEvidenceFragment = 200

// jscpdReport mirrors the JSON report written by
// `jscpd <dir> --reporters json --output <dir>` (captured 5.0.16 output).
type jscpdReport struct {
	Duplicates []jscpdClone `json:"duplicates"`
	Statistics jscpdStats   `json:"statistics"`
}

// jscpdClone is one duplicated block pair.
type jscpdClone struct {
	FirstFile  jscpdFileRef `json:"firstFile"`
	SecondFile jscpdFileRef `json:"secondFile"`
	Format     string       `json:"format"`
	Fragment   string       `json:"fragment"`
	Lines      int          `json:"lines"`
	Tokens     int          `json:"tokens"`
}

// jscpdFileRef locates one side of a clone. Name is relative to the scanned
// directory (e.g. "sub1/a.js"), which equals the repo-relative path by
// construction.
type jscpdFileRef struct {
	Name     string   `json:"name"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	StartLoc jscpdLoc `json:"startLoc"`
	EndLoc   jscpdLoc `json:"endLoc"`
}

// jscpdLoc is a line/column/position location.
type jscpdLoc struct {
	Column   int `json:"column"`
	Line     int `json:"line"`
	Position int `json:"position"`
}

// jscpdStats carries the aggregate report statistics (kept for schema
// fidelity; the check consumes the duplicates list).
type jscpdStats struct {
	Total jscpdTotals `json:"total"`
}

// jscpdTotals aggregates clone counts across the scanned corpus.
type jscpdTotals struct {
	Clones             int     `json:"clones"`
	DuplicatedLines    int     `json:"duplicatedLines"`
	DuplicatedTokens   int     `json:"duplicatedTokens"`
	Lines              int     `json:"lines"`
	NewClones          int     `json:"newClones"`
	NewDuplicatedLines int     `json:"newDuplicatedLines"`
	Percentage         float64 `json:"percentage"`
	PercentageTokens   float64 `json:"percentageTokens"`
	Sources            int     `json:"sources"`
	Tokens             int     `json:"tokens"`
}

// commandRunner executes a subprocess and returns its captured stdout, stderr,
// and exit code. err is non-nil only when the process failed to launch; a
// non-zero exit code is reported through exitCode, not err. Tests inject a
// fake runner so they never depend on the real jscpd binary.
type commandRunner func(ctx context.Context, name string, args []string, workdir string) (stdout, stderr string, exitCode int, err error)

// Check implements service.Check by delegating duplication detection to
// jscpd.
type Check struct {
	client *kern.KernClient // used by the in-house triage and fallback (may be nil)

	runner         commandRunner
	binary         string
	argsPrefix     []string // e.g. ["--yes", "jscpd@5"] when running via npx
	binaryExplicit bool

	version    string // cached `jscpd --version` output
	versionSet bool

	// confirmFn is the Pass-2 confirmation oracle for block-eligible in-house
	// candidates. nil (default) correlates against the actual jscpd clone
	// report; tests and benchmarks inject a mock so they never depend on the
	// real jscpd binary agreeing with the in-house check.
	confirmFn func(filePair [2]string) bool
}

// Option configures a Check. Options exist for tests and embedders that need
// to pin the binary path or inject a fake subprocess runner.
type Option func(*Check)

// WithBinary forces the jscpd binary path. An empty string disables the
// incumbent, which makes Run fall back to the in-house structural check.
func WithBinary(path string) Option {
	return func(c *Check) {
		c.binary = path
		c.binaryExplicit = true
	}
}

// WithRunner injects the subprocess runner (tests).
func WithRunner(r commandRunner) Option {
	return func(c *Check) { c.runner = r }
}

// WithVersion pins the jscpd version string stamped on findings, skipping the
// `jscpd --version` probe (tests).
func WithVersion(v string) Option {
	return func(c *Check) { c.version = v; c.versionSet = true }
}

// WithConfirmer injects the Pass-2 confirmation oracle: given a canonical
// file pair ([2]string, sorted), it reports whether jscpd confirmed a clone
// for that pair. The default (nil) correlates against the actual jscpd clone
// report; tests inject a mock so the two-pass escalation can be exercised
// without a real jscpd binary.
func WithConfirmer(fn func(filePair [2]string) bool) Option {
	return func(c *Check) { c.confirmFn = fn }
}

// NewCheck constructs a jscpd-backed duplication check. client is used only
// for the in-house fallback when the jscpd binary is unavailable (may be nil).
func NewCheck(client *kern.KernClient, opts ...Option) *Check {
	c := &Check{client: client, runner: execCommand}
	for _, o := range opts {
		o(c)
	}
	if !c.binaryExplicit {
		c.binary, c.argsPrefix = resolveBinary()
	}
	return c
}

// Name returns the stable check identifier used for policy routing. The
// "duplication:" prefix routes this check to the duplication category
// (policy.categoryFromCheck splits on the first colon).
func (c *Check) Name() string { return "duplication:jscpd" }

// Run detects duplication introduced by the change with jscpd. Changed files
// (proposed content or on-disk) and the existing repo tree are mirrored into
// a temp dir preserving repo-relative paths; jscpd scans the mirror and only
// clones that involve a changed file are reported (new-change principle —
// pre-existing repo-internal duplication is not this change's signal). When
// the jscpd binary is unavailable, Run degrades to the in-house structural
// check and flags the fallback with a WARN finding.
func (c *Check) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}

	// No changed files => nothing to compare: PASS without invoking jscpd.
	if len(req.Files) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	// Incumbent unavailable => pure advisory fallback: the in-house structural
	// check runs with no confirmation possible, so every finding stays WARN,
	// and a WARN flag is added so the fallback is never silent.
	if c.binary == "" {
		return c.fallback(ctx, req)
	}

	// Pass 1 — in-house structural triage (always runs, advisory candidates)
	// plus the non-deleted changed-file filter (see inHouseTriage).
	advisory, changed := c.inHouseTriage(ctx, req)
	if len(changed) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}, nil
	}

	// Mirror the existing repo tree (the comparison corpus) plus the changed
	// files at their exact repo-relative paths.
	scanDir, changedSet, err := c.mirrorCorpus(req, changed)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
	}
	defer os.RemoveAll(scanDir)

	// Run jscpd against the mirror and parse its JSON report (fail closed on
	// malformed output, G14 contract).
	reportDir, report, version, err := c.runJscpd(ctx, scanDir)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: err.Error()}, nil
	}
	defer os.RemoveAll(reportDir)

	// Pass 2 — correlate block-eligible candidates with jscpd clones (see
	// correlatePassTwo).
	out := c.correlatePassTwo(advisory, report, changedSet, version)

	status := domain.StatusPass
	for _, f := range out {
		if f.Severity == domain.SeverityBlock {
			// Two-pass confirmed duplication is the first BLOCK this check
			// can produce; a single one forces the result to BLOCK.
			status = domain.StatusBlock
			break
		}
	}
	if status == domain.StatusPass && len(out) > 0 {
		status = domain.StatusWarn
	}
	return domain.CheckResult{Name: c.Name(), Status: status, Findings: dedupe(out)}, nil
}

// inHouseTriage runs Pass 1 of the two-pass model: the in-house structural
// duplication check (duplication:advisory) produces advisory candidates with
// confidence scores — above BlockCandidateThreshold (>0.90) they are
// block-eligible; below that they are advisory WARN and never block. Triage
// is best-effort: a nil client or a kern failure simply yields no candidates —
// jscpd remains the primary detector and the check still runs (no escalation
// is possible without a candidate). It also filters the change to the
// non-deleted files that the mirror must carry.
func (c *Check) inHouseTriage(ctx context.Context, req domain.ChangeRequest) (advisory []domain.Finding, changed []domain.FileChange) {
	if c.client != nil {
		if inner, err := duplication.NewCheck(c.client).Run(ctx, req); err == nil && inner.Status != domain.StatusError {
			advisory = inner.Findings
		}
	}

	for _, fc := range req.Files {
		if fc.Op != domain.OpDelete {
			changed = append(changed, fc)
		}
	}
	return advisory, changed
}

// mirrorCorpus mirrors the existing repo tree (the comparison corpus) plus
// the changed files at their exact repo-relative paths into a temp scan dir,
// and returns the set of changed paths (normalized) that the correlation pass
// uses for the new-change principle. Changed paths are confined to the temp
// dir before anything is written: anything that would escape it via ".." or
// an absolute path is rejected (same pattern as the in-house duplication
// check's content path). The caller must remove the returned dir on exit; on
// error it is removed here.
func (c *Check) mirrorCorpus(req domain.ChangeRequest, changed []domain.FileChange) (scanDir string, changedSet map[string]bool, err error) {
	scanDir, err = os.MkdirTemp("", "blueprint-jscpd-*")
	if err != nil {
		return "", nil, fmt.Errorf("jscpd: create scan dir: %w", err)
	}
	fail := func(err error) (string, map[string]bool, error) {
		_ = os.RemoveAll(scanDir)
		return "", nil, err
	}
	if err := copyRepo(req.RepositoryRoot, scanDir); err != nil {
		return fail(fmt.Errorf("jscpd: mirror repo: %w", err))
	}

	changedSet = make(map[string]bool, len(changed))
	for _, fc := range changed {
		rel := filepath.Clean(fc.Path)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fail(fmt.Errorf("invalid path in change request: %q", fc.Path))
		}

		content := fc.Content
		if content == "" {
			b, err := os.ReadFile(filepath.Join(req.RepositoryRoot, fc.Path))
			if err != nil {
				return fail(fmt.Errorf("jscpd: read %s: %w", fc.Path, err))
			}
			content = string(b)
		}

		dest := filepath.Join(scanDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fail(fmt.Errorf("jscpd: mkdir %s: %w", fc.Path, err))
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return fail(fmt.Errorf("jscpd: write %s: %w", fc.Path, err))
		}
		changedSet[normalizePath(fc.Path)] = true
	}
	return scanDir, changedSet, nil
}

// runJscpd executes the jscpd binary against the mirrored scan dir and parses
// its JSON report. The report output dir lives OUTSIDE the scanned dir so
// jscpd never rescans its own report; the caller must remove it on exit (on
// error it is removed here). A malformed report is a tool failure, never a
// silent pass (G14 contract: fail closed).
func (c *Check) runJscpd(ctx context.Context, scanDir string) (reportDir string, report jscpdReport, version string, err error) {
	reportDir, err = os.MkdirTemp("", "blueprint-jscpd-report-*")
	if err != nil {
		return "", jscpdReport{}, "", fmt.Errorf("jscpd: create report dir: %w", err)
	}
	fail := func(err error) (string, jscpdReport, string, error) {
		_ = os.RemoveAll(reportDir)
		return "", jscpdReport{}, "", err
	}

	args := []string{
		scanDir,
		"--min-tokens", strconv.Itoa(minTokens),
		"--reporters", "json",
		"--output", reportDir,
	}
	if len(c.argsPrefix) > 0 {
		args = append(append([]string{}, c.argsPrefix...), args...)
	}
	_, stderr, code, runErr := c.runner(ctx, c.binary, args, scanDir)
	if runErr != nil {
		return fail(fmt.Errorf("jscpd: %w", runErr))
	}
	if code != 0 {
		return fail(fmt.Errorf("jscpd failed (exit %d): %s", code, strings.TrimSpace(stderr)))
	}

	data, err := os.ReadFile(filepath.Join(reportDir, "jscpd-report.json"))
	if err != nil {
		return fail(fmt.Errorf("jscpd: read report: %w", err))
	}
	if err := json.Unmarshal(data, &report); err != nil {
		// Fail closed on malformed output (G14 contract): a report we cannot
		// parse is a tool failure, never a silent pass.
		return fail(fmt.Errorf("jscpd: parse report: %w", err))
	}

	return reportDir, report, c.jscpdVersion(ctx), nil
}

// correlatePassTwo runs Pass 2 of the two-pass model against the jscpd clone
// report. A high-confidence in-house candidate (>0.90) whose file pair also
// appears as a jscpd clone is CONFIRMED and escalates to BLOCK
// (duplication:confirmed-block), replacing both the in-house advisory and the
// jscpd clone for that pair. A candidate jscpd does not confirm stays advisory
// WARN (in-house thought high similarity, jscpd disagreed — not enough to
// block). jscpd-only clones stay WARN (duplication:jscpd:clone). For pairs
// neither side escalated, both signals are kept — they are different evidence
// (structural vs token-based). Clones that involve no changed file are dropped
// (new-change principle: pre-existing repo-internal duplication is not this
// change's signal).
func (c *Check) correlatePassTwo(advisory []domain.Finding, report jscpdReport, changedSet map[string]bool, version string) []domain.Finding {
	var out []domain.Finding
	escalated := make(map[[2]string]bool) // canonical pairs replaced by a confirmed-block

	for _, f := range advisory {
		if !duplication.BlockEligible(f.Confidence) {
			continue // handled below as a standard advisory
		}
		pair, ok := advisoryFilePair(f)
		if !ok {
			// Cannot correlate (unexpected evidence layout): keep the
			// advisory as-is rather than dropping a signal.
			out = append(out, f)
			continue
		}
		pair = canonicalPair(pair[0], pair[1]) // set membership is direction-agnostic
		matches, confirmed := c.matchingClones(pair, report.Duplicates)
		if !confirmed {
			// jscpd does NOT confirm: stays advisory WARN.
			out = append(out, f)
			continue
		}
		escalated[pair] = true
		out = append(out, c.confirmedFinding(f, matches, version))
	}

	// Standard in-house advisories (0.60-0.90, never block). A standard
	// advisory for an escalated pair is redundant with the confirmed-block
	// (the pair is already proven duplicated) and is dropped.
	for _, f := range advisory {
		if duplication.BlockEligible(f.Confidence) {
			continue
		}
		if pair, ok := advisoryFilePair(f); ok && escalated[canonicalPair(pair[0], pair[1])] {
			continue
		}
		out = append(out, f)
	}

	// jscpd clones. Escalated pairs are replaced by the confirmed-block;
	// everything else stays WARN.
	for _, cl := range report.Duplicates {
		file := normalizePath(cl.FirstFile.Name)
		other := normalizePath(cl.SecondFile.Name)

		// New-change principle: only report clones that involve a changed
		// file; pre-existing repo-internal duplication is not this change's
		// signal (mirrors the in-house check, which only flags new functions).
		if !changedSet[file] && !changedSet[other] {
			continue
		}
		if escalated[canonicalPair(file, other)] {
			continue
		}
		out = append(out, cloneFinding(cl, version))
	}
	return out
}

// fallback degrades to the in-house structural duplication check when jscpd
// is not installed, and flags the degradation with a WARN finding so it is
// never silent. An in-house BLOCK or ERROR wins over the fallback WARN (fail
// closed, monotonic); anything else surfaces the WARN fallback signal.
func (c *Check) fallback(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	res := domain.CheckResult{Name: c.Name(), Status: domain.StatusPass}
	if c.client != nil {
		inner, err := duplication.NewCheck(c.client).Run(ctx, req)
		if err != nil {
			return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "in-house fallback: " + err.Error()}, nil
		}
		res = inner
		res.Name = c.Name()
	}
	res.Findings = append(res.Findings, domain.Finding{
		RuleID:      "duplication:incumbent-unavailable",
		Severity:    domain.SeverityWarn,
		Category:    domain.CategoryDuplication,
		Message:     "jscpd not found; falling back to in-house check",
		Explanation: "The jscpd binary is not installed (set JSCPD_BINARY, add jscpd/cpd to $PATH, or rely on npx). Results come from the in-house structural duplication check instead. Install jscpd to get its 200+ language coverage.",
		RuleVersion: "1",
		Confidence:  1.0,
		Scope:       "repo",
		Evidence: []domain.Evidence{{
			Kind:        "fallback",
			Description: "jscpd unavailable; in-house structural duplication check used",
		}},
	})
	if res.Status != domain.StatusBlock && res.Status != domain.StatusError {
		res.Status = domain.StatusWarn
	}
	return res, nil
}

// cloneFinding builds the WARN finding for a jscpd clone that was not
// escalated by the two-pass model (RuleID duplication:jscpd:clone).
func cloneFinding(cl jscpdClone, version string) domain.Finding {
	file := normalizePath(cl.FirstFile.Name)
	other := normalizePath(cl.SecondFile.Name)
	return domain.Finding{
		RuleID:       "duplication:jscpd:clone",
		Severity:     domain.SeverityWarn, // duplication is probabilistic (Rule 4)
		Category:     domain.CategoryDuplication,
		File:         file,
		Line:         cl.FirstFile.StartLoc.Line,
		Message:      fmt.Sprintf("Duplicated code block (%d tokens, %d lines) also found in %s:%d", cl.Tokens, cl.Lines, other, cl.SecondFile.StartLoc.Line),
		Explanation:  fmt.Sprintf("jscpd detected a %s clone: %d tokens duplicated across %d lines between %s and %s. Consider extracting the shared logic.", cl.Format, cl.Tokens, cl.Lines, file, other),
		SuggestedFix: fmt.Sprintf("Extract the shared logic into one helper and call it from both %s and %s.", file, other),
		RuleVersion:  version,
		Confidence:   cloneConfidence(cl.Tokens),
		Scope:        "file",
		Evidence: []domain.Evidence{{
			Kind:        "jscpd-clone",
			Description: fmt.Sprintf("format: %s, tokens: %d, lines: %d; fragment:\n%s", cl.Format, cl.Tokens, cl.Lines, truncate(cl.Fragment, maxEvidenceFragment)),
			Location:    fmt.Sprintf("%s:%d vs %s:%d", file, cl.FirstFile.StartLoc.Line, other, cl.SecondFile.StartLoc.Line),
		}},
	}
}

// advisoryFilePair extracts the file pair from an in-house advisory finding
// (duplication:advisory). The new file is Finding.File; the existing file is
// parsed from the structural-fingerprint evidence Location, whose layout is
// "<new>:<line> (new) vs <existing>:<line> (existing)". The pair is returned
// in natural order [new, existing]; use canonicalPair for set membership.
func advisoryFilePair(f domain.Finding) ([2]string, bool) {
	var loc string
	for _, e := range f.Evidence {
		if e.Kind == "structural-fingerprint" {
			loc = e.Location
			break
		}
	}
	if loc == "" {
		return [2]string{}, false
	}
	const sep = " (new) vs "
	idx := strings.Index(loc, sep)
	if idx < 0 {
		return [2]string{}, false
	}
	existing, ok := stripLocationLine(loc[idx+len(sep):])
	if !ok {
		return [2]string{}, false
	}
	return [2]string{normalizePath(f.File), normalizePath(existing)}, true
}

// stripLocationLine removes the ":<line>" suffix from a "<path>:<line>"
// location fragment (also tolerating a trailing " (existing)" marker).
func stripLocationLine(s string) (string, bool) {
	rest := strings.TrimSuffix(s, " (existing)")
	if rest == s {
		return "", false // missing the " (existing)" marker — unexpected layout
	}
	if idx := strings.LastIndex(rest, ":"); idx > 0 {
		return rest[:idx], true
	}
	return "", false
}

// canonicalPair returns the file pair with paths sorted, so set membership is
// independent of which side jscpd or the in-house check reported first.
func canonicalPair(a, b string) [2]string {
	if a <= b {
		return [2]string{a, b}
	}
	return [2]string{b, a}
}

// matchingClones returns the jscpd clones involving the canonical pair plus
// the confirmation verdict. With the default oracle (confirmFn nil), the
// verdict is "confirmed" iff at least one report clone involves the pair.
// With an injectable confirmer (WithConfirmer), the confirmer is the oracle:
// a mock that says yes confirms even when the report carries no matching
// clone (the report clones, if any, are still returned as evidence); a mock
// that says no never confirms.
func (c *Check) matchingClones(pair [2]string, clones []jscpdClone) ([]jscpdClone, bool) {
	var m []jscpdClone
	for _, cl := range clones {
		if canonicalPair(normalizePath(cl.FirstFile.Name), normalizePath(cl.SecondFile.Name)) == pair {
			m = append(m, cl)
		}
	}
	if c.confirmFn != nil {
		return m, c.confirmFn(pair)
	}
	return m, len(m) > 0
}

// confirmedFinding builds the escalated duplication:confirmed-block finding
// that replaces both the in-house advisory and the jscpd clone for a pair.
// Its evidence carries both signals (kind two-pass-confirmed): the structural
// similarity that made the candidate block-eligible and the jscpd clone(s)
// that confirmed it. Confidence is the max of the two detectors' scores.
func (c *Check) confirmedFinding(advisory domain.Finding, matches []jscpdClone, version string) domain.Finding {
	conf := advisory.Confidence
	evidence := make([]domain.Evidence, 0, len(matches)+2)
	evidence = append(evidence, domain.Evidence{
		Kind:        "structural-fingerprint",
		Description: advisory.Evidence[0].Description,
		Location:    advisory.Evidence[0].Location,
	})
	for _, cl := range matches {
		if cc := cloneConfidence(cl.Tokens); cc > conf {
			conf = cc
		}
		evidence = append(evidence, domain.Evidence{
			Kind:        "jscpd-clone",
			Description: fmt.Sprintf("format: %s, tokens: %d, lines: %d; fragment:\n%s", cl.Format, cl.Tokens, cl.Lines, truncate(cl.Fragment, maxEvidenceFragment)),
			Location:    fmt.Sprintf("%s:%d vs %s:%d", normalizePath(cl.FirstFile.Name), cl.FirstFile.StartLoc.Line, normalizePath(cl.SecondFile.Name), cl.SecondFile.StartLoc.Line),
		})
	}
	evidence = append(evidence, domain.Evidence{
		Kind:        "two-pass-confirmed",
		Description: "high-confidence structural candidate confirmed by jscpd clone evidence",
	})

	pair, _ := advisoryFilePair(advisory)
	return domain.Finding{
		RuleID:       "duplication:confirmed-block",
		Severity:     domain.SeverityBlock,
		Category:     domain.CategoryDuplication,
		File:         advisory.File,
		Line:         advisory.Line,
		Message:      fmt.Sprintf("duplicate confirmed: %s is a confirmed duplicate of %s (structural similarity %.2f + jscpd clone)", pair[0], pair[1], advisory.Confidence),
		Explanation:  fmt.Sprintf("The in-house structural check scored a function in %s above the block-eligible threshold (%.2f) and jscpd independently detected a clone in the same file pair, so the duplication is two-pass confirmed. Reuse the existing implementation instead of reimplementing it.", pair[0], advisory.Confidence),
		SuggestedFix: advisory.SuggestedFix,
		RuleVersion:  version,
		Confidence:   conf,
		Scope:        "file",
		Evidence:     evidence,
	}
}

// jscpdVersion returns the installed jscpd version (best-effort, probed once
// per check instance via `jscpd --version`; the v5 output has a leading
// "cpd " prefix that is stripped). An empty string on probe failure means
// findings are stamped without a version rather than failing the validation.
func (c *Check) jscpdVersion(ctx context.Context) string {
	if c.versionSet {
		return c.version
	}
	c.versionSet = true
	if c.binary == "" {
		return ""
	}
	args := []string{"--version"}
	if len(c.argsPrefix) > 0 {
		args = append(append([]string{}, c.argsPrefix...), "--version")
	}
	out, _, code, err := c.runner(ctx, c.binary, args, "")
	if err != nil || code != 0 {
		return ""
	}
	c.version = strings.TrimSpace(strings.TrimPrefix(out, "cpd "))
	return c.version
}

// resolveBinary locates the jscpd executable: JSCPD_BINARY env var, then
// `jscpd` on $PATH, then `cpd` (the v5 binary name), then `npx jscpd@5`.
// Returns ("", nil) when unavailable (the check then falls back to the
// in-house structural check).
func resolveBinary() (string, []string) {
	if p := os.Getenv("JSCPD_BINARY"); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		return "", nil
	}
	if p, err := exec.LookPath("jscpd"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("cpd"); err == nil {
		return p, nil
	}
	if p, err := exec.LookPath("npx"); err == nil {
		// --yes skips the "Ok to proceed?" prompt on first use.
		return p, []string{"--yes", "jscpd@5"}
	}
	return "", nil
}

// excludedDirs are skipped when mirroring the repo into the scan dir: the
// check's own state, VCS metadata, and common vendored/heavy build trees that
// jscpd should never treat as comparison corpus.
var excludedDirs = map[string]bool{
	".git": true, ".blueprint": true, "node_modules": true, "vendor": true,
	".venv": true, "venv": true, "dist": true, "build": true, "target": true,
	"coverage": true, "Pods": true, ".build": true, ".cache": true,
	".gradle": true, ".idea": true, ".vscode": true, "__pycache__": true,
	".next": true, ".turbo": true,
}

// copyRepo mirrors the repository tree (excluding excludedDirs) into dst so
// jscpd can compare changed files against the existing codebase. Symlinks are
// skipped (never followed) so a link cannot escape dst.
func copyRepo(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			if excludedDirs[info.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil // skip symlinks, sockets, fifos
		}
		return copyFile(path, filepath.Join(dst, rel))
	})
}

// copyFile copies one regular file, creating parent directories as needed.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// dedupe collapses findings for the same clone pair (file, line, other file,
// other line), keeping the first: jscpd emits one entry per pair, so a
// changed file duplicated against several existing files yields one finding
// per distinct counterpart — the intended UX (mirrors the in-house check's
// dedupe, which keeps one finding per distinct match).
func dedupe(findings []domain.Finding) []domain.Finding {
	seen := make(map[string]bool, len(findings))
	out := findings[:0]
	for _, f := range findings {
		key := clonePairKey(f)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// clonePairKey derives a unique key for a clone finding from its location and
// counterpart location (both carried in the finding's evidence Location).
func clonePairKey(f domain.Finding) string {
	loc := ""
	if len(f.Evidence) > 0 {
		loc = normalizePath(f.Evidence[0].Location)
	}
	return normalizePath(f.File) + ":" + strconv.Itoa(f.Line) + "|" + loc
}

// cloneConfidence derives a 0..1 confidence from the clone's token count:
// longer clones are more likely to be true (non-coincidental) duplication.
// jscpd itself does not express a score, so the estimate is monotonic in
// tokens and capped at 0.95.
func cloneConfidence(tokens int) float64 {
	if tokens <= 0 {
		return 0.8
	}
	conf := 0.5 + float64(tokens)/400.0
	if conf > 0.95 {
		return 0.95
	}
	return conf
}

// truncate caps s at max characters, appending "..." when cut.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// normalizePath canonicalizes a path for set membership and reporting:
// forward slashes, no leading "./" or "/".
func normalizePath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	return strings.TrimLeft(p, "/")
}

// execCommand is the default commandRunner. It runs the process with the
// given working directory and returns the captured stdout, stderr, and exit
// code. err is non-nil only for launch failures, not for non-zero exits.
func execCommand(ctx context.Context, name string, args []string, workdir string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workdir
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	if runErr == nil {
		return outBuf.String(), errBuf.String(), 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		// The process ran; its exit code is a result, not a launch failure.
		return outBuf.String(), errBuf.String(), exitErr.ExitCode(), nil
	}
	return outBuf.String(), errBuf.String(), -1, runErr
}
