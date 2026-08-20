package learning

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// seedMemory populates the store with the given memories and returns it.
func seedMemory(t *testing.T, ms []domain.Memory) *memory.MemoryStore {
	t.Helper()
	store := memory.NewMemoryStore(t.TempDir())
	for _, m := range ms {
		if _, err := store.Add(m); err != nil {
			t.Fatalf("Add(%q) failed: %v", m.Content, err)
		}
	}
	return store
}

// incident returns a deterministic incident memory with an explicit timestamp.
func incident(content, scope string, at time.Time) domain.Memory {
	return domain.Memory{
		Type:      domain.MemoryIncident,
		Content:   content,
		Source:    "sre",
		Scope:     scope,
		CreatedAt: at,
	}
}

func TestPatternExtractionGroupsByScope(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := seedMemory(t, []domain.Memory{
		incident("checkout latency spike", "service:checkout", base),
		incident("checkout timeout", "service:checkout", base.Add(1*time.Hour)),
		incident("checkout crash loop", "service:checkout", base.Add(2*time.Hour)),
		incident("payments rejected", "service:payments", base.Add(3*time.Hour)),
	})

	ex := New(store)
	patterns, err := ex.Patterns()
	if err != nil {
		t.Fatalf("Patterns() failed: %v", err)
	}

	if len(patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d: %+v", len(patterns), patterns)
	}

	// Count desc means service:checkout (3) sorts before service:payments (1).
	top := patterns[0]
	if top.Key != "scope:service:checkout" {
		t.Fatalf("expected first pattern key scope:service:checkout, got %q", top.Key)
	}
	if top.Count != 3 {
		t.Fatalf("expected checkout count 3, got %d", top.Count)
	}
	if top.Sample[0] != "checkout crash loop" {
		t.Fatalf("expected newest checkout sample first, got %q", top.Sample[0])
	}
	if len(top.Sample) != 3 {
		t.Fatalf("expected checkout sample capped at 3, got %d", len(top.Sample))
	}
	if len(top.Scopes) != 1 || top.Scopes[0] != "service:checkout" {
		t.Fatalf("expected distinct scope [service:checkout], got %v", top.Scopes)
	}
}

func TestPatternExtractionGroupsBySignature(t *testing.T) {
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	// No Scope -> grouped by stable content signature; two identical contents
	// (after trimming) share one signature.
	store := seedMemory(t, []domain.Memory{
		{Type: domain.MemoryIncident, Content: "  duplicate failure  ", CreatedAt: base},
		{Type: domain.MemoryIncident, Content: "duplicate failure", CreatedAt: base.Add(time.Hour)},
		{Type: domain.MemoryLesson, Content: "unique lesson", CreatedAt: base.Add(2 * time.Hour)},
	})

	ex := New(store)
	patterns, err := ex.Patterns()
	if err != nil {
		t.Fatalf("Patterns() failed: %v", err)
	}
	if len(patterns) != 2 {
		t.Fatalf("expected 2 signature patterns, got %d: %+v", len(patterns), patterns)
	}
	// The duplicate-content group has Count 2 and must sort first.
	if patterns[0].Count != 2 {
		t.Fatalf("expected duplicate group count 2, got %d", patterns[0].Count)
	}
	if patterns[0].Key != "sig:"+contentHash12("duplicate failure") {
		t.Fatalf("unexpected signature key %q", patterns[0].Key)
	}
}

func TestPatternIncidentsAndRecommendation(t *testing.T) {
	p := Pattern{
		Key:            "scope:svc:checkout",
		Count:          3,
		Sample:         []string{"N+1 query in checkout loop"},
		Incidents:      []string{"INC-492", "INC-381"},
		Recommendation: "Use batch/eager loading",
	}
	if len(p.Incidents) != 2 || p.Incidents[0] != "INC-492" {
		t.Errorf("Incidents = %v, want [INC-492 INC-381]", p.Incidents)
	}
	if p.Recommendation != "Use batch/eager loading" {
		t.Errorf("Recommendation = %q, want \"Use batch/eager loading\"", p.Recommendation)
	}
}

func TestSurfaceThreshold(t *testing.T) {
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	store := seedMemory(t, []domain.Memory{
		incident("a1", "service:a", base),
		incident("a2", "service:a", base.Add(time.Hour)),
		incident("b1", "service:b", base.Add(2*time.Hour)),
	})

	ex := New(store)
	surfaced, err := ex.Surface(2)
	if err != nil {
		t.Fatalf("Surface(2) failed: %v", err)
	}
	if len(surfaced) != 1 {
		t.Fatalf("expected 1 surfaced pattern, got %d: %+v", len(surfaced), surfaced)
	}
	if surfaced[0].Key != "scope:service:a" || surfaced[0].Count != 2 {
		t.Fatalf("unexpected surfaced pattern: %+v", surfaced[0])
	}

	// threshold <= 0 is treated as 1 -> both patterns returned.
	all, err := ex.Surface(0)
	if err != nil {
		t.Fatalf("Surface(0) failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 patterns for threshold 0, got %d", len(all))
	}
}

func TestRemember(t *testing.T) {
	store := memory.NewMemoryStore(t.TempDir())
	ex := New(store)
	at := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	p := Pattern{
		Key:     "scope:service:checkout",
		Count:   3,
		Scopes:  []string{"service:checkout"},
		Sample:  []string{"x"},
		Created: at,
	}
	got, err := ex.Remember(p)
	if err != nil {
		t.Fatalf("Remember() failed: %v", err)
	}
	if got.ID == "" {
		t.Fatal("expected Remember() to return a memory with an ID")
	}
	if got.Type != domain.MemoryConstraint {
		t.Fatalf("expected type MemoryConstraint, got %q", got.Type)
	}
	if got.Source != "learning" {
		t.Fatalf("expected source learning, got %q", got.Source)
	}

	// Verify it was actually persisted and retrievable via the store.
	list, err := store.List("")
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 stored memory, got %d", len(list))
	}
	got = list[0]
	if got.Type != domain.MemoryConstraint || got.Source != "learning" {
		t.Fatalf("stored memory mismatch: %+v", got)
	}
	expectedContent := "pattern: scope:service:checkout recurring 3 times across 1 scope(s)"
	if got.Content != expectedContent {
		t.Fatalf("unexpected content:\n got %q\nwant %q", got.Content, expectedContent)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "pattern" || got.Tags[1] != "auto-surfaced" {
		t.Fatalf("unexpected tags: %v", got.Tags)
	}
}
