package main

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/architecture"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestArchDriftRecordsMapping proves the deterministic finding→record
// conversion: each per-subsystem drift condition of the parity report maps to
// one ArchitectureDriftRecord with the validator's own kind string, healthy
// subsystems contribute nothing, and a nil report yields no records.
func TestArchDriftRecordsMapping(t *testing.T) {
	report := &architecture.ArchDocReport{
		Rows: 2,
		Findings: []architecture.ArchDrift{
			{
				// LOC + deps drift on one subsystem -> 2 records.
				Subsystem:  "internal/web",
				LOC:        5200,
				Cap:        4700,
				Violations: []string{"internal/gates", "internal/optimize"},
			},
			{
				// Structural problem only -> 1 degraded-warn record.
				Subsystem: "internal/blueprint",
				Err:       "directory missing",
			},
			{
				// Healthy row -> nothing.
				Subsystem: "internal/domain",
				LOC:       2000,
				Cap:       3200,
			},
		},
	}
	records := archDriftRecords(report)
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3: %+v", len(records), records)
	}
	got := map[string][]domain.ArchitectureDriftRecord{}
	for _, r := range records {
		if r.At.IsZero() {
			t.Errorf("record %s/%s has zero At", r.Subsystem, r.ViolationKind)
		}
		got[r.Subsystem] = append(got[r.Subsystem], r)
	}

	web := got["internal/web"]
	if len(web) != 2 {
		t.Fatalf("internal/web records = %d, want 2: %+v", len(web), web)
	}
	kinds := map[string]string{}
	for _, r := range web {
		kinds[r.ViolationKind] = r.Change
	}
	if kinds["loc-over-cap"] != "" {
		t.Errorf("loc-over-cap Change = %q, want empty (no change ref)", kinds["loc-over-cap"])
	}
	if kinds["import-not-in-allowed-deps"] != "internal/gates, internal/optimize" {
		t.Errorf("import-not-in-allowed-deps Change = %q, want the joined imports", kinds["import-not-in-allowed-deps"])
	}

	bp := got["internal/blueprint"]
	if len(bp) != 1 {
		t.Fatalf("internal/blueprint records = %d, want 1: %+v", len(bp), bp)
	}
	if bp[0].ViolationKind != "degraded-warn" {
		t.Errorf("internal/blueprint kind = %q, want degraded-warn", bp[0].ViolationKind)
	}

	if _, ok := got["internal/domain"]; ok {
		t.Errorf("healthy subsystem produced records: %+v", got["internal/domain"])
	}
	if records := archDriftRecords(nil); records != nil {
		t.Fatalf("nil report produced records: %+v", records)
	}
}

// TestArchDriftSectionFindsRecords proves the doctor wiring runs the parity
// check once and yields both the renderable findings and the drift records on
// a real fixture, and that the findings keep their non-fatal warn/ok levels.
func TestArchDriftSectionFindsRecords(t *testing.T) {
	dir := t.TempDir()
	findings, records := archDriftSection(dir)
	// A fresh temp dir has no ARCHITECTURE.md: the parity run fails closed.
	if len(findings) != 1 || findings[0].Level != "fail" {
		t.Fatalf("findings = %+v, want one fail finding (missing ARCHITECTURE.md)", findings)
	}
	if records != nil {
		t.Fatalf("records = %+v, want nil on parity error", records)
	}
}
