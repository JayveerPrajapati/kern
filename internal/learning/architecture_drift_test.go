package learning

import (
	"reflect"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// driftRecord returns one observed drift sample with deterministic fields.
func driftRecord(subsystem, kind, change string, at time.Time) domain.ArchitectureDriftRecord {
	return domain.ArchitectureDriftRecord{
		Subsystem:     subsystem,
		ViolationKind: kind,
		Change:        change,
		At:            at,
	}
}

// TestArchitectureDriftPatternsRecommends proves the pre-flag signal: a
// (subsystem, violation kind) pair observed across >= threshold records
// yields ONE RECOMMENDATION pattern carrying the deterministic statement, the
// "drift:<subsystem>:<kind>" scope, the record count, and the deduped sorted
// change refs as provenance.
func TestArchitectureDriftPatternsRecommends(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ArchitectureDriftRecord{
		driftRecord("internal/web", "import-not-in-allowed-deps", "internal/gates", base),
		driftRecord("internal/web", "import-not-in-allowed-deps", "internal/gates", base.Add(time.Hour)),
		driftRecord("internal/web", "import-not-in-allowed-deps", "internal/optimize", base.Add(2*time.Hour)),
	}
	patterns := ArchitectureDriftPatterns(records, 3)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1: %+v", len(patterns), patterns)
	}
	p := patterns[0]
	if p.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", p.ClaimType)
	}
	if p.Count != 3 {
		t.Errorf("Count = %d, want 3", p.Count)
	}
	wantKey := "drift:internal/web:import-not-in-allowed-deps"
	if p.Key != wantKey {
		t.Errorf("Key = %q, want %q", p.Key, wantKey)
	}
	if len(p.Scopes) != 1 || p.Scopes[0] != wantKey {
		t.Errorf("Scopes = %v, want [%q]", p.Scopes, wantKey)
	}
	wantStmt := "changes touching subsystem internal/web are prone to import-not-in-allowed-deps violations (3 recorded) — pre-flag at plan time"
	if p.Statement != wantStmt {
		t.Errorf("Statement = %q, want %q", p.Statement, wantStmt)
	}
	// Provenance: deduped sorted change refs, newest observation time.
	wantSrcs := []string{"internal/gates", "internal/optimize"}
	if !reflect.DeepEqual(p.Provenance.Sources, wantSrcs) {
		t.Errorf("Provenance.Sources = %v, want %v", p.Provenance.Sources, wantSrcs)
	}
	if p.Provenance.Latest != base.Add(2*time.Hour) {
		t.Errorf("Provenance.Latest = %v, want latest record time", p.Provenance.Latest)
	}
	if p.Created != base.Add(2*time.Hour) {
		t.Errorf("Created = %v, want newest record time", p.Created)
	}
}

// TestArchitectureDriftPatternsLabelProvenance proves that records without a
// change ref fall back to the deterministic subsystem/kind label as their
// provenance source, and that deduplication collapses repeated labels.
func TestArchitectureDriftPatternsLabelProvenance(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ArchitectureDriftRecord{
		driftRecord("internal/app", "loc-over-cap", "", base),
		driftRecord("internal/app", "loc-over-cap", "", base.Add(time.Hour)),
		driftRecord("internal/app", "loc-over-cap", "", base.Add(2*time.Hour)),
	}
	patterns := ArchitectureDriftPatterns(records, 3)
	if len(patterns) != 1 {
		t.Fatalf("patterns = %d, want 1: %+v", len(patterns), patterns)
	}
	p := patterns[0]
	wantSrcs := []string{"drift internal/app (loc-over-cap)"}
	if !reflect.DeepEqual(p.Provenance.Sources, wantSrcs) {
		t.Errorf("Provenance.Sources = %v, want %v", p.Provenance.Sources, wantSrcs)
	}
	if p.Provenance.Count != 3 {
		t.Errorf("Provenance.Count = %d, want 3", p.Provenance.Count)
	}
}

// TestArchitectureDriftPatternsBelowThresholdNothing proves the threshold
// guard: a (subsystem, kind) pair below threshold yields no patterns, and an
// empty record set yields nothing even at threshold 1.
func TestArchitectureDriftPatternsBelowThresholdNothing(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ArchitectureDriftRecord{
		driftRecord("internal/web", "loc-over-cap", "", base),
		driftRecord("internal/web", "loc-over-cap", "", base.Add(time.Hour)),
	}
	if patterns := ArchitectureDriftPatterns(records, 3); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (below threshold): %+v", len(patterns), patterns)
	}
	if patterns := ArchitectureDriftPatterns(nil, 1); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (empty records)", len(patterns))
	}
	// Records with empty subsystem/kind are skipped, never grouped.
	skip := []domain.ArchitectureDriftRecord{{Change: "x", At: base}}
	if patterns := ArchitectureDriftPatterns(skip, 1); len(patterns) != 0 {
		t.Fatalf("patterns = %d, want 0 (blank subsystem/kind skipped)", len(patterns))
	}
}

// TestArchitectureDriftPatternsDeterministic proves determinism: shuffled
// input yields the same patterns (same keys, statements, counts, provenance)
// as the original order, and results are sorted by scope then statement.
func TestArchitectureDriftPatternsDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := []domain.ArchitectureDriftRecord{
		driftRecord("internal/web", "import-not-in-allowed-deps", "internal/gates", base),
		driftRecord("internal/app", "loc-over-cap", "", base.Add(time.Hour)),
		driftRecord("internal/web", "import-not-in-allowed-deps", "internal/gates", base.Add(2*time.Hour)),
		driftRecord("internal/app", "loc-over-cap", "", base.Add(3*time.Hour)),
		driftRecord("internal/web", "import-not-in-allowed-deps", "internal/optimize", base.Add(4*time.Hour)),
		driftRecord("internal/app", "loc-over-cap", "", base.Add(5*time.Hour)),
	}
	shuffled := []domain.ArchitectureDriftRecord{
		records[4], records[1], records[5], records[0], records[3], records[2],
	}
	first := ArchitectureDriftPatterns(records, 3)
	second := ArchitectureDriftPatterns(shuffled, 3)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("patterns differ across input order:\nfirst:  %+v\nsecond: %+v", first, second)
	}
	if len(first) != 2 {
		t.Fatalf("patterns = %d, want 2", len(first))
	}
	if first[0].Key > first[1].Key {
		t.Errorf("patterns not sorted by scope: %q before %q", first[0].Key, first[1].Key)
	}
}
