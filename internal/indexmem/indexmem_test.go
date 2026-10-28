package indexmem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadMemoizesAndInvalidates pins the contract a warm process depends on:
// a matching fingerprint is served from memory, a changed store re-hydrates,
// and a failed load is never memoized.
func TestLoadMemoizesAndInvalidates(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	dir := t.TempDir()
	store := filepath.Join(dir, "index.sqlite")
	paths := []string{store}

	calls := 0
	load := func() (string, error) { calls++; return "hydrated", nil }

	for i := 0; i < 3; i++ {
		got, err := Load("root", paths, load)
		if err != nil || got != "hydrated" {
			t.Fatalf("load %d: %q %v", i, got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected one hydration, got %d", calls)
	}

	// The store appears/changes (a rebuild): the memo must be dropped.
	if err := os.WriteFile(store, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("root", paths, load); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected re-hydration after store change, got %d", calls)
	}
}

// TestLoadDoesNotMemoizeErrors pins that a failed hydration is retried instead
// of being cached as failure.
func TestLoadDoesNotMemoizeErrors(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	fail := true
	calls := 0
	load := func() (int, error) {
		calls++
		if fail {
			return 0, errors.New("store unavailable")
		}
		return 42, nil
	}
	paths := []string{filepath.Join(t.TempDir(), "missing.sqlite")}
	if _, err := Load("root", paths, load); err == nil {
		t.Fatal("expected the first load to fail")
	}
	fail = false
	got, err := Load("root", paths, load)
	if err != nil || got != 42 {
		t.Fatalf("retry: got %d err=%v", got, err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 load attempts, got %d", calls)
	}
}

// TestFingerprintStableWithoutStore pins that a missing store has a stable
// fingerprint (a daemon must not re-hydrate on every request for a repo whose
// index lives only in the JSON fallback it never touches).
func TestFingerprintStableWithoutStore(t *testing.T) {
	paths := []string{filepath.Join(t.TempDir(), "nope.sqlite")}
	if fingerprint(paths) != fingerprint(paths) {
		t.Fatal("fingerprint must be stable for a missing store")
	}
}
