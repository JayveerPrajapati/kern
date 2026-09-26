package retrieval

import (
	"os"
	"path/filepath"
	"testing"
)

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

// TestRegistrySaveLoadRoundTrip verifies a handle survives the process
// boundary: Save in one registry instance, Load in a fresh one (what
// `kern retrieve` -> `kern resolve` now rely on).
func TestRegistrySaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handles.json")

	r1 := NewRegistry()
	h := NewHandle(TypeSymbol, "TaskService", "internal/app/task.go", 29, 27, 1.0, "abc")
	r1.Register(h)
	if err := r1.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r2 := NewRegistry()
	r2.Load(path)
	got, ok := r2.Resolve(h.ID)
	if !ok {
		t.Fatal("handle not resolvable after Load in fresh registry")
	}
	if got.Name != "TaskService" || got.Source != "internal/app/task.go" || got.Line != 29 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestRegistryLoadCorruptIsBestEffort ensures a corrupt store leaves the
// registry empty (resolve then fails with the usual unknown-handle message)
// rather than panicking or failing loudly.
func TestRegistryLoadCorruptIsBestEffort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handles.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.Load(path) // must not panic
	if r.Len() != 0 {
		t.Fatalf("Len = %d after corrupt load, want 0", r.Len())
	}
}

// TestRegistrySaveMergesOnDisk guards the cross-process handle lifecycle
// (QA Pick #2, F-R1): each `kern retrieve` runs in a fresh process with an
// empty registry. Before the merge-on-disk fix, Save wrote only that
// process's handles, orphaning every handle persisted by earlier processes
// (10 entries -> 1 after the next retrieve). A fresh registry that saves
// must carry over handles already in the store.
func TestRegistrySaveMergesOnDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handles.json")

	// Process A registers two handles and saves.
	a := NewRegistry()
	for _, name := range []string{"FuncA1", "FuncA2"} {
		h := NewHandle(TypeSymbol, name, "pkg/a.go", 10, 20, 0.9, "aaa")
		a.Register(h)
	}
	if err := a.Save(path); err != nil {
		t.Fatalf("save A: %v", err)
	}

	// Process B (fresh, empty registry) registers one different handle and
	// saves to the same store.
	b := NewRegistry()
	hb := NewHandle(TypeSymbol, "FuncB1", "pkg/b.go", 30, 40, 0.9, "bbb")
	b.Register(hb)
	if err := b.Save(path); err != nil {
		t.Fatalf("save B: %v", err)
	}

	// A fresh process C must be able to load and resolve ALL three handles.
	c := NewRegistry()
	c.Load(path)
	for _, id := range []string{a.List()[0].ID, a.List()[1].ID, hb.ID} {
		if _, ok := c.Resolve(id); !ok {
			t.Fatalf("handle %s orphaned by fresh-process save (F-R1 regression)", id)
		}
	}
}

// TestRegistrySaveInMemoryWinsOnConflict pins the merge precedence: when the
// saving registry re-registers a handle with the same ID as a persisted one,
// the in-memory version is written.
func TestRegistrySaveInMemoryWinsOnConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handles.json")

	a := NewRegistry()
	a.Register(NewHandle(TypeSymbol, "Dup", "same.go", 1, 2, 0.9, "ccc"))
	if err := a.Save(path); err != nil {
		t.Fatal(err)
	}

	b := NewRegistry()
	fresh := NewHandle(TypeSymbol, "Dup", "same.go", 1, 2, 0.9, "ddd")
	b.Register(fresh)
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}

	c := NewRegistry()
	c.Load(path)
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (one handle per ID)", c.Len())
	}
	if got, _ := c.Resolve(a.List()[0].ID); got == nil || got.ContentHash != "ddd" {
		t.Fatalf("in-memory version did not win: %+v", got)
	}
}
