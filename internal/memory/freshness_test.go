package memory

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestRecallExcludesSupersededMemory is the exit gate: stale knowledge
// cannot be silently presented as current fact. After a memory is superseded,
// the default Recall path must NOT return it (it is no longer current), while
// an explicit IncludeNonCurrent query may still retrieve it for audit.
func TestRecallExcludesSupersededMemory(t *testing.T) {
	s := NewMemoryStore(t.TempDir())

	old := domain.Memory{Type: domain.MemoryLesson, Scope: "service:payments", Content: "payments use v1 API"}
	if _, err := s.Add(old); err != nil {
		t.Fatalf("Add old: %v", err)
	}
	// Supersede: the new memory replaces the old one (same type+scope).
	if _, err := s.Supersede(domain.Memory{Type: domain.MemoryLesson, Scope: "service:payments", Content: "payments use v2 API"}); err != nil {
		t.Fatalf("Supersede: %v", err)
	}

	// Default Recall: only the CURRENT memory must surface.
	current, err := s.Recall(Query{Type: domain.MemoryLesson, Scope: "service:payments"})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	for _, m := range current {
		if m.Status == domain.MemorySuperseded || m.Status == domain.MemoryHistorical {
			t.Fatalf("default Recall returned stale memory: %+v (exit gate)", m)
		}
	}
	if len(current) != 1 {
		t.Fatalf("default Recall returned %d memories, want 1 (the current one)", len(current))
	}
	if current[0].Status != domain.MemoryCurrent {
		t.Errorf("recalled memory status = %q, want current (15.4)", current[0].Status)
	}

	// Audit path: IncludeNonCurrent retrieves the full history.
	all, err := s.Recall(Query{Type: domain.MemoryLesson, Scope: "service:payments", IncludeNonCurrent: true})
	if err != nil {
		t.Fatalf("Recall(IncludeNonCurrent): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("IncludeNonCurrent Recall returned %d memories, want 2 (current + superseded)", len(all))
	}
}

// TestMemoryCarriesFreshnessMetadata verifies the metadata is
// recorded on recalled memory: created_at is populated and the state is one of
// current / historical / superseded (15.2/15.4), so consumers can judge
// freshness instead of assuming it.
func TestMemoryCarriesFreshnessMetadata(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	added, err := s.Add(domain.Memory{Type: domain.MemoryLesson, Scope: "service:x", Content: "lesson"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.CreatedAt.IsZero() {
		t.Error("memory created_at is zero (15.1)")
	}
	if added.Status != domain.MemoryCurrent {
		t.Errorf("new memory status = %q, want current", added.Status)
	}
	if added.ID == "" {
		t.Error("memory id empty")
	}
}
