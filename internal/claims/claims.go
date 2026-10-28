// Package claims implements `kern verify claims`: the deterministic,
// gate-backed proof of kern's public product claims.
//
// The premise is structural, not cosmetic. A claim without a machine check
// rots silently: the 2026-10-26/27 audits found the README promising a
// deterministic blast radius and a tamper-evident audit chain while the live
// binary returned a confident false all-clear and printed a tamper warning.
// Every claim registered here therefore maps one public statement to a probe
// that fails when the statement stops being true, so a release can no longer
// ship marketing the artifact does not meet.
//
// Claims are deterministic and local: no LLM, no network, no host command
// execution. They read the index, the audit store, the guard classifier and
// the tool catalog — the same primitives the user-facing commands use.
package claims

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Status is a claim's outcome. There are exactly three: a claim is proven,
// disproven, or not applicable to this repository (with the reason recorded).
// "Not applicable" is never silently folded into PASS — an unproven claim
// must be visible in the report.
type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
	StatusSkip Status = "SKIP"
)

// Result is one claim's outcome plus the evidence that decided it.
type Result struct {
	ID        string   `json:"id"`
	Statement string   `json:"statement"`
	Status    Status   `json:"status"`
	Detail    string   `json:"detail,omitempty"`
	Evidence  []string `json:"evidence,omitempty"`
}

// Report is the whole claims run. OK is true only when no claim failed;
// skipped claims do not fail the run but are counted so a suspiciously
// vacuous gate (everything skipped) is visible at a glance.
type Report struct {
	Root    string   `json:"root"`
	Claims  []Result `json:"claims"`
	OK      bool     `json:"ok"`
	Failed  int      `json:"failed"`
	Skipped int      `json:"skipped"`
	Elapsed string   `json:"elapsed,omitempty"`
}

// Run executes every registered claim against root. The index is loaded once
// and shared; an index failure fails the claims that need it instead of
// aborting the run, so the independent claims still report.
func Run(root string) Report {
	start := time.Now()
	// Resolve the root BEFORE any claim runs. The guard-corpus claim classifies
	// write commands with the path-aware arm, which resolves relative targets
	// against an absolute root; passing "." made every in-tree move look
	// out-of-tree and failed a claim the binary actually satisfies (found
	// 2026-10-27 while extending the corpus with `mv internal/index
	// internal/index2`).
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	rep := Report{Root: root}
	ix, ixErr := index.LoadOrBuild(root)
	rep.Claims = []Result{
		claimBlastRadius(ix, ixErr),
		claimImpactRisk(root, ix, ixErr),
		claimSecuritySelfScan(root, ix, ixErr),
		claimAuditChain(root),
		claimGateCorpus(root),
		claimToolCatalog(),
	}
	rep.OK = true
	for _, c := range rep.Claims {
		switch c.Status {
		case StatusFail:
			rep.OK = false
			rep.Failed++
		case StatusSkip:
			rep.Skipped++
		}
	}
	rep.Elapsed = time.Since(start).Round(time.Millisecond).String()
	return rep
}

// fail builds a FAIL result with its evidence.
func fail(id, statement, detail string, evidence ...string) Result {
	return Result{ID: id, Statement: statement, Status: StatusFail, Detail: detail, Evidence: evidence}
}

// pass builds a PASS result with its evidence.
func pass(id, statement, detail string, evidence ...string) Result {
	return Result{ID: id, Statement: statement, Status: StatusPass, Detail: detail, Evidence: evidence}
}

// skip builds a SKIP result. The reason is required: a claim that could not be
// exercised must say why, so a vacuous gate reads as vacuous.
func skip(id, statement, reason string) Result {
	return Result{ID: id, Statement: statement, Status: StatusSkip, Detail: reason}
}

// String renders one claim as a single deterministic report line.
func (r Result) String() string {
	if r.Detail == "" {
		return fmt.Sprintf("%-5s %-32s %s", r.Status, r.ID, r.Statement)
	}
	return fmt.Sprintf("%-5s %-32s %s — %s", r.Status, r.ID, r.Statement, r.Detail)
}
