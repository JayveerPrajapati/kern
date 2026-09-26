package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestRecordArchitectureDriftWritesRecommendation proves the full learning
// pass: enough drift records for one (subsystem, violation kind) pair write
// exactly one RECOMMENDATION typed-claim memory through the learning path
// (deterministic "pre-flag at plan time" statement, "drift:<sub>:<kind>"
// scope, change refs as provenance), and a second identical batch upserts the
// same scope instead of duplicating (idempotent).
func TestRecordArchitectureDriftWritesRecommendation(t *testing.T) {
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	records := make([]domain.ArchitectureDriftRecord, 0, DefaultArchitectureDriftThreshold)
	for i := 0; i < DefaultArchitectureDriftThreshold; i++ {
		records = append(records, domain.ArchitectureDriftRecord{
			Subsystem:     "internal/web",
			ViolationKind: "import-not-in-allowed-deps",
			Change:        "internal/gates",
			At:            base.Add(time.Duration(i) * time.Hour),
		})
	}
	root := t.TempDir()
	mem := memory.NewMemoryStore(root)
	n, err := RecordArchitectureDrift(records, mem, DefaultArchitectureDriftThreshold)
	if err != nil {
		t.Fatalf("RecordArchitectureDrift: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}
	mems, err := mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List: %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.ClaimType != domain.ClaimRecommendation {
		t.Errorf("ClaimType = %q, want RECOMMENDATION", m.ClaimType)
	}
	if m.Scope != "drift:internal/web:import-not-in-allowed-deps" {
		t.Errorf("Scope = %q, want drift:internal/web:import-not-in-allowed-deps", m.Scope)
	}
	if !strings.Contains(m.Content, "changes touching subsystem internal/web are prone to import-not-in-allowed-deps violations (3 recorded) — pre-flag at plan time") {
		t.Errorf("Content = %q, want the pre-flag statement", m.Content)
	}
	if !strings.Contains(m.Provenance, "internal/gates") {
		t.Errorf("Provenance = %q, want the change refs", m.Provenance)
	}
	// Second identical batch: accumulator grows to 6 records for the pair, so
	// the same scope is upserted — no duplicate memory.
	n2, err := RecordArchitectureDrift(records, mem, DefaultArchitectureDriftThreshold)
	if err != nil {
		t.Fatalf("RecordArchitectureDrift (2nd): %v", err)
	}
	if n2 != 1 {
		t.Fatalf("written (2nd) = %d, want 1 (upsert)", n2)
	}
	mems, err = mem.List(domain.MemoryConstraint)
	if err != nil {
		t.Fatalf("memory List (2nd): %v", err)
	}
	if len(mems) != 1 {
		t.Fatalf("memories (2nd) = %d, want 1 (idempotent upsert, no duplicate)", len(mems))
	}
}

// TestRecordArchitectureDriftNilGuard proves a nil memory store is a no-op
// that never panics (0, nil) — unwired paths keep their zero behavior change.
func TestRecordArchitectureDriftNilGuard(t *testing.T) {
	records := []domain.ArchitectureDriftRecord{
		{Subsystem: "internal/web", ViolationKind: "loc-over-cap", At: time.Now()},
	}
	n, err := RecordArchitectureDrift(records, nil, DefaultArchitectureDriftThreshold)
	if err != nil {
		t.Fatalf("nil mem returned error: %v", err)
	}
	if n != 0 {
		t.Fatalf("nil mem written = %d, want 0", n)
	}
}
