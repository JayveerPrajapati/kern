package agent

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func mkAgent(id, name, typ string) Agent {
	return Agent{
		Agent: domain.Agent{
			ID:        id,
			Name:      name,
			Type:      typ,
			CreatedAt: time.Now(),
		},
	}
}

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	a := mkAgent("a1", "Alice", "coder")
	if err := r.Register(a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("a1")
	if !ok {
		t.Fatal("Get(a1): not found")
	}
	if got.Name != "Alice" || got.Type != "coder" {
		t.Fatalf("Get(a1) = %+v, want Alice/coder", got)
	}
}

func TestRegistryGetNotFound(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("nope"); ok {
		t.Fatal("Get(unknown) found an agent; want not found")
	}
}

func TestRegistryRegisterEmptyID(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(mkAgent("", "NoID", "coder")); err == nil {
		t.Fatal("Register with empty ID: want error (fail closed)")
	}
}

func TestRegistryRegisterDuplicate(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(mkAgent("a1", "Alice", "coder")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(mkAgent("a1", "Alice2", "coder")); err == nil {
		t.Fatal("duplicate Register: want error")
	}
}

func TestRegistryByType(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(mkAgent("a2", "Bob", "reviewer"))
	_ = r.Register(mkAgent("a1", "Alice", "coder"))
	_ = r.Register(mkAgent("a3", "Carol", "coder"))

	coders := r.ByType("coder")
	if len(coders) != 2 {
		t.Fatalf("ByType(coder) len = %d, want 2", len(coders))
	}
	if coders[0].ID != "a1" || coders[1].ID != "a3" {
		t.Fatalf("ByType(coder) order = %q,%q, want sorted a1,a3", coders[0].ID, coders[1].ID)
	}
	if got := r.ByType("sre"); len(got) != 0 {
		t.Fatalf("ByType(sre) len = %d, want 0", len(got))
	}
}

func TestRegistryAllSorted(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(mkAgent("c", "C", "coder"))
	_ = r.Register(mkAgent("a", "A", "coder"))
	_ = r.Register(mkAgent("b", "B", "coder"))
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() len = %d, want 3", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID > all[i].ID {
			t.Fatalf("All() not sorted: %q before %q", all[i-1].ID, all[i].ID)
		}
	}
	if all[0].ID != "a" || all[2].ID != "c" {
		t.Fatalf("All() = %q,%q,%q, want a,b,c", all[0].ID, all[1].ID, all[2].ID)
	}
}
