package retrieval

import "testing"

func TestNewHandleStableID(t *testing.T) {
	h1 := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 5, 1.0, "hash1")
	h2 := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 5, 1.0, "hash1")
	if h1.ID != h2.ID {
		t.Errorf("identical inputs produced different IDs: %q vs %q", h1.ID, h2.ID)
	}
	if len(h1.ID) != 64 {
		t.Errorf("expected SHA-256 hex ID of length 64, got %d", len(h1.ID))
	}
}

func TestNewHandleLineChangesID(t *testing.T) {
	h1 := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 5, 1.0, "hash1")
	h2 := NewHandle(TypeSymbol, "greet", "a/b.go", 11, 5, 1.0, "hash1")
	if h1.ID == h2.ID {
		t.Errorf("different lines produced the same ID: %q", h1.ID)
	}
}

func TestRegistryLifecycle(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("fresh registry Len = %d, want 0", r.Len())
	}
	h := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 5, 1.0, "hash1")
	r.Register(h)
	if got, ok := r.Resolve(h.ID); !ok || got != h {
		t.Fatalf("Resolve after Register = (%v, %v), want (%v, true)", got, ok, h)
	}
	if r.Len() != 1 {
		t.Fatalf("Len = %d, want 1", r.Len())
	}
	// Register overwrites the handle with the same ID.
	h2 := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 9, 1.0, "hash2")
	r.Register(h2)
	if got, ok := r.Resolve(h.ID); !ok || got != h2 {
		t.Fatalf("overwrite failed: got %v (ok=%v), want h2", got, ok)
	}
	if r.Len() != 1 {
		t.Fatalf("Len after overwrite = %d, want 1", r.Len())
	}
	r.Invalidate(h.ID)
	if _, ok := r.Resolve(h.ID); ok {
		t.Fatalf("Resolve after Invalidate should be false")
	}
	if r.Len() != 0 {
		t.Fatalf("Len after Invalidate = %d, want 0", r.Len())
	}
}

func TestRegistryResolveUnknown(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Resolve("nope"); ok {
		t.Fatalf("Resolve of unknown ID should return false")
	}
}

func TestRegistryListSorted(t *testing.T) {
	r := NewRegistry()
	h1 := NewHandle(TypeSymbol, "a", "a/a.go", 1, 1, 1.0, "h")
	h2 := NewHandle(TypeSymbol, "b", "b/b.go", 1, 1, 1.0, "h")
	h3 := NewHandle(TypeSymbol, "c", "c/c.go", 1, 1, 1.0, "h")
	for _, h := range []*Handle{h3, h1, h2} {
		r.Register(h)
	}
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("List len = %d, want 3", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID > list[i].ID {
			t.Fatalf("List not sorted at %d: %q > %q", i, list[i-1].ID, list[i].ID)
		}
	}
}
