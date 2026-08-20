package memory

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestRecallTaskRepositoryArchitectureFilters verifies the Task, Repository
// and Architecture query filters are honored (matched against scope prefixes).
func TestRecallTaskRepositoryArchitectureFilters(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	seed := []domain.Memory{
		{Type: domain.MemoryDecision, Content: "task t1 decision", Scope: "task:t1", Source: "agent"},
		{Type: domain.MemoryDecision, Content: "task t2 decision", Scope: "task:t2", Source: "agent"},
		{Type: domain.MemoryLesson, Content: "repo kernel lesson", Scope: "repository:kernel", Source: "agent"},
		{Type: domain.MemorySemantic, Content: "arch note", Scope: "architecture:http-latency", Source: "agent"},
		{Type: domain.MemoryLesson, Content: "unrelated lesson", Scope: "service:payments", Source: "agent"},
	}
	for _, m := range seed {
		if _, err := s.Add(m); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	t.Run("Task", func(t *testing.T) {
		got, err := s.Recall(Query{Task: "t1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Scope != "task:t1" {
			t.Fatalf("Task filter = %+v, want 1 task:t1 memory", got)
		}
	})
	t.Run("Repository", func(t *testing.T) {
		got, err := s.Recall(Query{Repository: "kernel"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Scope != "repository:kernel" {
			t.Fatalf("Repository filter = %d, want 1 repository:kernel memory", len(got))
		}
	})
	t.Run("Architecture", func(t *testing.T) {
		got, err := s.Recall(Query{Architecture: "http-latency"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Scope != "architecture:http-latency" {
			t.Fatalf("Architecture filter = %d, want 1 architecture memory", len(got))
		}
	})
	t.Run("EmptyFiltersReturnAll", func(t *testing.T) {
		got, err := s.Recall(Query{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 5 {
			t.Fatalf("empty query = %d, want 5", len(got))
		}
	})
}

// TestRecallNewFiltersBackwardCompatible confirms existing filters still work
// alongside the new ones.
func TestRecallNewFiltersBackwardCompatible(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	if _, err := s.Add(domain.Memory{Type: domain.MemoryLesson, Content: "service memory", Scope: "service:payments", Source: "agent"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Recall(Query{Service: "payments"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Service filter still broken: %d (err %v)", len(got), err)
	}
}

func TestParseScope(t *testing.T) {
	cases := []struct{ scope, wantKind, wantID string }{
		{"", "global", ""},
		{"service:payments", "service", "payments"},
		{"module:auth", "module", "auth"},
		{"project", "", "project"}, // no colon
	}
	for _, c := range cases {
		kind, id := ParseScope(c.scope)
		if string(kind) != c.wantKind || id != c.wantID {
			t.Errorf("ParseScope(%q) = (%q, %q), want (%q, %q)", c.scope, kind, id, c.wantKind, c.wantID)
		}
	}
}

func TestQueryByModuleScope(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	s.Add(domain.Memory{ID: "m1", Scope: "module:auth", Content: "auth memory"})
	s.Add(domain.Memory{ID: "m2", Scope: "module:billing", Content: "billing memory"})
	got, _ := s.Recall(Query{Module: "auth"})
	if len(got) != 1 || got[0].ID != "m1" {
		t.Errorf("Recall(Module=auth) = %v, want [m1]", got)
	}
}

func TestQueryGlobalScope(t *testing.T) {
	s := NewMemoryStore(t.TempDir())
	s.Add(domain.Memory{ID: "g1", Scope: "", Content: "global"})
	s.Add(domain.Memory{ID: "g2", Scope: "service:payments", Content: "scoped"})
	got, _ := s.Recall(Query{Global: true})
	if len(got) != 1 || got[0].ID != "g1" {
		t.Errorf("Recall(Global=true) = %v, want [g1]", got)
	}
}
