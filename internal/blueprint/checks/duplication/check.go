// Package duplication provides the in-house structural duplication check.
//
// The in-house duplication check is advisory-only. Primary duplication
// detection is delegated to jscpd (see adapters/jscpd); this check serves as
// Pass-1 triage in the two-pass model (advisory candidates, scores >0.90 are
// block-eligible but escalate only when jscpd confirms — see adapters/jscpd)
// and as the pure-advisory fallback when jscpd is unavailable. Benchmark
// results (see docs/duplication-benchmark.md) show precision 0.50 / FPR 0.75
// at the 0.60 threshold — not production-grade — so it is intentionally
// WARN-only and should not be treated as a definitive duplicate detector.
package duplication

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// Fingerprint is a structural summary of a Go function, independent of
// identifier names (spec line 1049: "Do not use raw text equality as the main
// signal"). It captures:
//   - Signature shape (param/return arity and types, normalized)
//   - Control-flow shape (if/for/switch/return counts)
//   - Called symbols (normalized to arity)
//   - Literal structure (number of string/int/bool literals)
//   - Statement count (rough size proxy)
//
// Blueprint no longer computes fingerprints itself: it receives them from
// `kern fingerprint` (see kern.FingerprintRecord) and converts each record
// with fingerprintFromRecord. ControlFlow holds the control-flow shape
// counts that kern's fingerprint record emits (control_flow) and feeds
// controlFlowSimilarity as part of the overall similarity score.
type Fingerprint struct {
	FuncName       string // original name (for reporting only)
	SignatureShape string // normalized signature, e.g. "func(1ptr,1int)1err"
	ParamCount     int    // number of parameters
	ReturnCount    int    // number of return values
	ControlFlow    CFFingerprint
	CalledSymbols  []string // normalized call signatures
	LiteralCount   int      // total literals
	StatementCount int      // total statements
}

// CFFingerprint captures control-flow shape counts emitted by kern's
// fingerprint record (control_flow). It feeds controlFlowSimilarity, which
// compares the vectors with cosine similarity as part of the overall score.
type CFFingerprint struct {
	IfCount     int
	ForCount    int
	RangeCount  int
	SwitchCount int
	ReturnCount int
	DeferCount  int
	GoCount     int
	AssignCount int
	CallCount   int
}

// Check implements the duplication check as a Blueprint service.Check (spec
// Phase 6). The duplication oracle lives in kern (`kern fingerprint`): this
// check consumes kern's structural fingerprints of functions in changed files
// and compares them against fingerprints of functions in existing (unchanged)
// files in the same repo. The similarity pipeline (similarity.go) stays in
// Blueprint.
//
// The check is probabilistic (spec Rule 4): findings are always WARN or INFO,
// never BLOCK. The spec explicitly forbids promoting to BLOCK until
// benchmark results justify it (line 1084). The benchmark
// (docs/duplication-benchmark.md) shows precision 0.50 / FPR 0.75 at the
// 0.60 threshold — below the production-grade target — so the check stays
// advisory-only (Name/RuleID "duplication:advisory"): primary duplication
// detection is delegated to jscpd, and this check is a fallback when jscpd
// is unavailable.
type Check struct {
	client *kern.KernClient
}

// NewCheck constructs a DuplicationCheck backed by the given kern client. A
// nil client makes Run return StatusError ("kern client required").
func NewCheck(client *kern.KernClient) *Check {
	return &Check{client: client}
}

// Name returns the stable check identifier for policy routing. The
// "advisory" suffix marks this as a fallback heuristic, not a definitive
// duplicate detector (see the package doc and docs/duplication-benchmark.md).
func (Check) Name() string { return "duplication:advisory" }

// Run executes the duplication check. It:
//  1. Splits changed .go files (non-delete, .go suffix, non-test) into
//     proposed-content files and on-disk files.
//  2. Fingerprints on-disk changed files via `kern fingerprint` scoped to
//     those files, and proposed-content files via a temp mirror of the
//     content (they are not on disk yet).
//  3. Fingerprints existing files via a whole-root scan (or the per-file
//     fingerprint cache when one is present), excluding changed files.
//  4. Compares each new function against all existing functions.
//  5. Emits WARN findings for similarity >= 0.60 (spec tiers).
func (c Check) Run(ctx context.Context, req domain.ChangeRequest) (domain.CheckResult, error) {
	if c.client == nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "kern client required"}, nil
	}
	if req.RepositoryRoot == "" {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "repository root required"}, nil
	}

	// Collect changed file paths (only .go, only non-deletions, no test
	// files). Files with proposed content (pre-write) are fingerprinted from
	// a temp mirror of the content; the rest are read from disk.
	contentByPath := make(map[string]string, len(req.Files))
	var diskFiles []string
	changedSet := make(map[string]bool, len(req.Files))
	for _, fc := range req.Files {
		if fc.Op == domain.OpDelete {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(fc.Path), ".go") {
			continue
		}
		if isTestFile(fc.Path) {
			continue
		}
		changedSet[fc.Path] = true
		if fc.Content != "" {
			contentByPath[fc.Path] = fc.Content
		} else {
			diskFiles = append(diskFiles, fc.Path)
		}
	}

	if len(changedSet) == 0 {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusSkip}, nil
	}

	// Fingerprint new functions (changed files).
	newFuncs, err := c.fingerprintChanged(ctx, req.RepositoryRoot, diskFiles, contentByPath)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "fingerprint changed files: " + err.Error()}, nil
	}

	// Fingerprint existing functions. A per-file cache under
	// .blueprint/fingerprint-cache/ (keyed by path + SHA-256 content hash)
	// serves unchanged files and re-fingerprints only the files whose content
	// changed; without a cache this falls back to the uncached whole-root
	// scan (identical results, just slower).
	existingFuncs, err := c.fingerprintExisting(ctx, req.RepositoryRoot, changedSet)
	if err != nil {
		return domain.CheckResult{Name: c.Name(), Status: domain.StatusError, Error: "fingerprint existing files: " + err.Error()}, nil
	}

	// Compare and emit findings.
	var findings []domain.Finding
	for _, nf := range newFuncs {
		var bestMatch *existingFunc
		var bestScore float64
		for i := range existingFuncs {
			ef := &existingFuncs[i]
			score := Similarity(nf.Fingerprint, ef.Fingerprint)
			if score > bestScore {
				bestScore = score
				bestMatch = ef
			}
		}
		if bestMatch != nil && bestScore >= 0.60 {
			bucket := Bucket(bestScore)
			severity := domain.SeverityWarn
			if bucket == "informational" {
				severity = domain.SeverityInfo
			}
			findings = append(findings, domain.Finding{
				RuleID:       "duplication:advisory",
				Severity:     severity,
				Category:     domain.CategoryDuplication,
				File:         nf.File,
				Line:         nf.Line,
				Message:      fmt.Sprintf("duplicate-candidate: %s (similarity %.2f) matches %s::%s", nf.FuncName, bestScore, bestMatch.File, bestMatch.FuncName),
				Explanation:  fmt.Sprintf("New function %s in %s is structurally similar (score %.2f, bucket %s) to existing function %s in %s. Consider reusing the existing function.", nf.FuncName, nf.File, bestScore, bucket, bestMatch.FuncName, bestMatch.File),
				SuggestedFix: fmt.Sprintf("Reuse %s::%s instead of reimplementing the same logic.", bestMatch.File, bestMatch.FuncName),
				RuleVersion:  "1",
				Confidence:   bestScore, // the structural similarity score that triggered the finding
				Scope:        "file",
				Evidence: []domain.Evidence{{
					Kind:        "structural-fingerprint",
					Description: fmt.Sprintf("similarity score: %.2f, bucket: %s", bestScore, bucket),
					Location:    fmt.Sprintf("%s:%d (new) vs %s:%d (existing)", nf.File, nf.Line, bestMatch.File, bestMatch.Line),
				}},
			})
		}
	}

	status := domain.StatusPass
	if len(findings) > 0 {
		// Duplication is always WARN or INFO, never BLOCK (spec line 1084).
		status = domain.StatusWarn
	}
	return domain.CheckResult{Name: c.Name(), Status: status, Findings: findings}, nil
}

// fingerprintChanged fingerprints the changed files: on-disk files are scanned
// at the repo root scoped to those files; proposed-content files are written
// to a temp mirror (at their exact repo-relative paths) and scanned there,
// mapping records back to the repo-relative path by construction. The temp
// mirror is removed before returning.
func (c Check) fingerprintChanged(ctx context.Context, repoRoot string, diskFiles []string, contentByPath map[string]string) ([]funcWithLocation, error) {
	var newFuncs []funcWithLocation

	if len(diskFiles) > 0 {
		recs, err := c.client.Fingerprints(ctx, repoRoot, diskFiles)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			newFuncs = append(newFuncs, funcWithLocation{
				Fingerprint: fingerprintFromRecord(rec),
				File:        rec.File,
				Line:        rec.Line,
			})
		}
	}

	if len(contentByPath) == 0 {
		return newFuncs, nil
	}

	tmpDir, err := os.MkdirTemp("", "blueprint-dup-content-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	relPaths := make([]string, 0, len(contentByPath))
	for path, content := range contentByPath {
		// Confine proposed paths to the temp dir: reject anything that would
		// escape it via ".." or an absolute path (same pattern as the secret
		// check's content path).
		rel := filepath.Clean(path)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return nil, fmt.Errorf("invalid path in proposed files: %q", path)
		}
		dest := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir for proposed content %s: %w", path, err)
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write proposed content %s: %w", path, err)
		}
		relPaths = append(relPaths, rel)
	}

	recs, err := c.client.Fingerprints(ctx, tmpDir, relPaths)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		newFuncs = append(newFuncs, funcWithLocation{
			Fingerprint: fingerprintFromRecord(rec),
			File:        rec.File, // == repo-relative path by construction
			Line:        rec.Line,
		})
	}
	return newFuncs, nil
}

// fingerprintFromRecord converts a kern fingerprint record into the
// duplication package's Fingerprint, mapping the record's control-flow counts
// into the fingerprint's CFFingerprint so the similarity score uses the full
// structural signal (identical to the original in-process oracle).
func fingerprintFromRecord(rec kern.FingerprintRecord) Fingerprint {
	return Fingerprint{
		FuncName:       rec.Name,
		SignatureShape: rec.SignatureShape,
		ParamCount:     rec.ParamCount,
		ReturnCount:    rec.ReturnCount,
		ControlFlow: CFFingerprint{
			IfCount:     rec.ControlFlow.If,
			ForCount:    rec.ControlFlow.For,
			RangeCount:  rec.ControlFlow.Range,
			SwitchCount: rec.ControlFlow.Switch,
			ReturnCount: rec.ControlFlow.Return,
			DeferCount:  rec.ControlFlow.Defer,
			GoCount:     rec.ControlFlow.Go,
			AssignCount: rec.ControlFlow.Assign,
			CallCount:   rec.ControlFlow.Call,
		},
		CalledSymbols:  rec.CalledSymbols,
		LiteralCount:   rec.LiteralCount,
		StatementCount: rec.StatementCount,
	}
}

// funcWithLocation pairs a Fingerprint with its source location.
type funcWithLocation struct {
	Fingerprint
	File string
	Line int
}

// existingFunc is an alias for clarity.
type existingFunc = funcWithLocation

// isTestFile returns true for _test.go files.
func isTestFile(rel string) bool {
	return strings.HasSuffix(strings.ToLower(rel), "_test.go")
}
