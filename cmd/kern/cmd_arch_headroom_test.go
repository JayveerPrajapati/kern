package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/architecture"
)

// TestHeadroomRowsSortsTightestFirst pins the report ordering and the
// threshold: only rows under headroomWarnPct appear, tightest first (an
// over-cap row sorts before a merely tight one), and the documented candidate
// is attached from the map.
func TestHeadroomRowsSortsTightestFirst(t *testing.T) {
	report := &architecture.ArchDocReport{Findings: []architecture.ArchDrift{
		{Subsystem: "internal/roomy", LOC: 100, Cap: 200},   // 50% headroom — excluded
		{Subsystem: "internal/tight", LOC: 990, Cap: 1000},  // 1% — included
		{Subsystem: "internal/exact", LOC: 1000, Cap: 1000}, // 0% — included
		{Subsystem: "internal/over", LOC: 1010, Cap: 1000},  // negative — included, first
		{Subsystem: "internal/broken", LOC: 0, Cap: 100, Err: "unmeasurable"},
	}}
	rows := headroomRows(report)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (roomy excluded, broken skipped): %+v", len(rows), rows)
	}
	wantOrder := []string{"internal/over", "internal/exact", "internal/tight"}
	for i, want := range wantOrder {
		if rows[i].Subsystem != want {
			t.Fatalf("row %d = %s, want %s (tightest first)", i, rows[i].Subsystem, want)
		}
	}
	// An undocumented subsystem must render the explicit "add one" pointer
	// instead of silently omitting the candidate.
	if got := rows[0].detail(); !strings.Contains(got, "no documented candidate") {
		t.Fatalf("undocumented candidate must be called out: %q", got)
	}
}

// TestExtractionCandidatesAreLedgerRows pins the candidate map against the
// real ledger: every documented candidate must name a subsystem that is still
// tight (so landing an extraction forces deleting the stale entry), and no
// entry may reference a package that has no row at all.
func TestExtractionCandidatesAreLedgerRows(t *testing.T) {
	root := "../.."
	rows, err := architecture.ParseArchDoc(root)
	if err != nil {
		t.Fatalf("parse ledger: %v", err)
	}
	caps := map[string]int{}
	for _, r := range rows {
		caps[r.Dir] = r.Cap
	}
	for dir := range extractionCandidates {
		cap, ok := caps[dir]
		if !ok {
			t.Errorf("extractionCandidates key %q has no ARCHITECTURE.md row — remove the stale entry", dir)
			continue
		}
		loc, _, _, err := architecture.MeasureDir(root + "/" + dir)
		if err != nil {
			t.Errorf("%s: measure: %v", dir, err)
			continue
		}
		pct := float64(cap-loc) / float64(cap) * 100
		if pct >= headroomWarnPct {
			t.Errorf("%s: documented candidate but headroom is %.2f%% (>= %.0f%%) — extraction landed (or cap moved); delete the stale candidate entry",
				dir, pct, headroomWarnPct)
		}
	}
}

// TestHeadroomCrossings pins the advisory gate's semantics: only a move FROM
// above the line TO at/below it reports; a row already tight at HEAD stays
// silent, a row that is still roomy stays silent, and a brand-new row (no
// HEAD entry) cannot cross.
func TestHeadroomCrossings(t *testing.T) {
	head := []architecture.ArchRow{
		{Dir: "internal/crossed", Cap: 1000},
		{Dir: "internal/already", Cap: 1000},
		{Dir: "internal/roomy", Cap: 1000},
		{Dir: "internal/grew", Cap: 1000},
	}
	headLOC := map[string]int{
		"internal/crossed": 900, // 10% -> 1%
		"internal/already": 995, // 0.5% -> 0.2% (no re-report)
		"internal/roomy":   500, // 50% -> 40%
		"internal/grew":    900, // 10% -> 10%
	}
	report := &architecture.ArchDocReport{Findings: []architecture.ArchDrift{
		{Subsystem: "internal/crossed", LOC: 990, Cap: 1000},
		{Subsystem: "internal/already", LOC: 998, Cap: 1000},
		{Subsystem: "internal/roomy", LOC: 600, Cap: 1000},
		{Subsystem: "internal/grew", LOC: 900, Cap: 1000},
		{Subsystem: "internal/brandnew", LOC: 999, Cap: 1000},
	}}
	got := headroomCrossings(report, head, headLOC)
	if len(got) != 1 || got[0].Subsystem != "internal/crossed" {
		t.Fatalf("crossings = %+v, want exactly internal/crossed", got)
	}
	if got[0].BeforePct <= headroomWarnPct || got[0].AfterPct > headroomWarnPct {
		t.Fatalf("crossing percentages wrong: %+v", got[0])
	}
}
