package main

import (
	"fmt"
	"sort"

	"github.com/JayveerPrajapati/kern/internal/architecture"
)

// Headroom policy (ARCHITECTURE.md § Headroom policy): a subsystem with less
// than headroomWarnPct of its cap left is "tight" — the next ordinary edit is
// likely to trip the ledger gate mid-task, so it needs a scheduled extraction
// rather than a cap raise. The doctor reports tight rows in the --arch-drift
// section, and --headroom-gate is the advisory CI check that fails only when
// a change MOVES a subsystem from above the line to at/below it.
const headroomWarnPct = 2.0

// extractionCandidates documents, per tight subsystem, the concrete family to
// move out when the package is next edited — the "documented extraction
// candidate" the policy requires. Single source of truth for the doctor's
// inline rendering; ARCHITECTURE.md states the rule, not the per-package list
// (no duplication to drift). Entries are evidence-based and must name a real
// family. When an extraction lands, delete the entry — pinned by
// TestExtractionCandidatesAreLedgerRows (stale key or recovered headroom fails).
var extractionCandidates = map[string]string{
	"internal/approvalcli":    "the ask/prompt and secret-line family (approval.go, passphrase.go, secretline_*.go) vs the approval record/key/policy commands — extract to internal/approvalui",
	"internal/doctor":         "the host-wiring checks (plugin sync, global-rules freshness, launch integrity, daemon identity) vs the environment/health probes — extract the wiring family to internal/doctorwire",
	"internal/governance":     "the Ask-Protocol grant layer (grants.go: prefix grants, gate approval mint/peek/claim) vs the approval record store and exec firewall — extract to internal/askgrant",
	"internal/index":          "the provenance/identity family (declaration IDs, call-site binding, compiler IDs) vs the parser/extractor core",
	"internal/intel":          "the next structure family after the community/hub extraction to internal/topology (see ARCHITECTURE.md)",
	"internal/mcp/provenance": "the provenance record/decorate helpers used by the dispatch layer",
	"internal/optimize":       "the log-compression family vs the prompt/output compressors",
}

// headroomRow is one subsystem's headroom measurement.
type headroomRow struct {
	Subsystem string
	LOC       int
	Cap       int
	Headroom  int     // Cap - LOC (negative when over cap)
	Pct       float64 // Headroom/Cap*100
	Candidate string  // documented extraction candidate ("" when undocumented)
}

// headroomRows returns the subsystems below headroomWarnPct, tightest first
// (negative headroom first — over-cap rows are the most urgent). Rows with an
// unmeasurable or zero cap are skipped: they carry an Err in the parity report
// and are surfaced there instead.
func headroomRows(report *architecture.ArchDocReport) []headroomRow {
	if report == nil {
		return nil
	}
	var rows []headroomRow
	for _, f := range report.Findings {
		if f.Cap <= 0 || f.Err != "" {
			continue
		}
		head := f.Cap - f.LOC
		pct := float64(head) / float64(f.Cap) * 100
		if pct >= headroomWarnPct {
			continue
		}
		rows = append(rows, headroomRow{
			Subsystem: f.Subsystem,
			LOC:       f.LOC,
			Cap:       f.Cap,
			Headroom:  head,
			Pct:       pct,
			Candidate: extractionCandidates[f.Subsystem],
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Pct != rows[j].Pct {
			return rows[i].Pct < rows[j].Pct
		}
		return rows[i].Subsystem < rows[j].Subsystem
	})
	return rows
}

// detail renders one headroom row for the doctor finding detail.
func (r headroomRow) detail() string {
	candidate := r.Candidate
	if candidate == "" {
		candidate = "no documented candidate — add one to extractionCandidates in cmd/kern/cmd_arch_headroom.go"
	}
	return fmt.Sprintf("%s: %d/%d LOC, %d line(s) headroom (%.2f%%) — extraction candidate: %s",
		r.Subsystem, r.LOC, r.Cap, r.Headroom, r.Pct, candidate)
}

// headroomCrossing is one subsystem that a working-tree change moved from
// acceptable (>headroomWarnPct of cap) to tight (<=headroomWarnPct) — the
// condition the advisory CI check fails on, forcing the author to record an
// extraction item in the same change instead of letting the package decay to
// zero headroom silently.
type headroomCrossing struct {
	Subsystem string
	BeforePct float64 // headroom percent at HEAD (from the recorded baseline)
	AfterPct  float64 // headroom percent measured in the working tree
	LOC, Cap  int
}

// headroomCrossings compares the working-tree parity report against a real
// measurement of HEAD (headLOC, from internal/gitrun-style `git grep -c`) and
// returns the subsystems that crossed the tight line. "Before" is HEAD's
// MEASURED LOC, not the ledger's baseline column: a stale baseline must never
// fake a crossing on a clean tree (the audit case: doctor --headroom-gate
// failed at HEAD for internal/guardrule and internal/index because the table's
// baseline column had drifted). "After" is the live measurement.
//
// Only rows present at HEAD participate: a brand-new subsystem has no "before"
// and cannot cross — it is governed by its initial cap. A row already tight at
// HEAD does not re-report (crossings, not membership), so the check fires once,
// when the change pushes a package over the line.
func headroomCrossings(report *architecture.ArchDocReport, headRows []architecture.ArchRow, headLOC map[string]int) []headroomCrossing {
	if report == nil {
		return nil
	}
	head := make(map[string]architecture.ArchRow, len(headRows))
	for _, row := range headRows {
		head[row.Dir] = row
	}
	var out []headroomCrossing
	for _, f := range report.Findings {
		if f.Err != "" || f.Cap <= 0 {
			continue
		}
		row, ok := head[f.Subsystem]
		if !ok || row.Cap <= 0 {
			continue
		}
		beforeLOC, measured := headLOC[row.Dir]
		if !measured {
			continue
		}
		before := float64(row.Cap-beforeLOC) / float64(row.Cap) * 100
		after := float64(f.Cap-f.LOC) / float64(f.Cap) * 100
		if before > headroomWarnPct && after <= headroomWarnPct {
			out = append(out, headroomCrossing{
				Subsystem: f.Subsystem,
				BeforePct: before,
				AfterPct:  after,
				LOC:       f.LOC,
				Cap:       f.Cap,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subsystem < out[j].Subsystem })
	return out
}

// detail renders one crossing for the doctor finding detail.
func (c headroomCrossing) detail() string {
	return fmt.Sprintf("%s: headroom %.2f%% -> %.2f%% (%d/%d LOC) — this change moved the subsystem under the %.0f%% line; add an extraction item (or extract now) and record its candidate in cmd/kern/cmd_arch_headroom.go",
		c.Subsystem, c.BeforePct, c.AfterPct, c.LOC, c.Cap, headroomWarnPct)
}
