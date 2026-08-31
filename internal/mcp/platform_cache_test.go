package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fixtureRoot builds a tiny single-package module in a temp dir so platform
// construction is fast and deterministic (no re-indexing of the whole repo).
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return root
}

// TestPlatformForCachesPerRoot verifies the Platform cache: repeated calls
// for the same root return the SAME *app.Platform instance (the whole point
// of the cache — high-level handlers previously rebuilt the graph + twin
// extracts on every tool call), while different roots get different
// instances.
func TestPlatformForCachesPerRoot(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{})
	root := fixtureRoot(t)

	p1, err := s.platformFor(context.Background(), root)
	if err != nil {
		t.Fatalf("platformFor #1: %v", err)
	}
	if p1 == nil {
		t.Fatal("platformFor returned nil")
	}
	p2, err := s.platformFor(context.Background(), root)
	if err != nil {
		t.Fatalf("platformFor #2: %v", err)
	}
	if p1 != p2 {
		t.Fatal("platformFor returned a DIFFERENT Platform instance for the same root — cache not effective")
	}

	other := fixtureRoot(t)
	p3, err := s.platformFor(context.Background(), other)
	if err != nil {
		t.Fatalf("platformFor #3 (other root): %v", err)
	}
	if p3 == p1 {
		t.Fatal("platformFor returned the same Platform for two different roots")
	}
}

// TestPlatformForRebuildsOnNewIndex verifies the invalidation contract: a
// Platform built from one index instance must not be served after the
// session's index instance changes. We simulate a rebuild by clearing the
// session cache entry and building a new index, then assert the platform is
// rebuilt (different instance).
func TestPlatformForRebuildsOnNewIndex(t *testing.T) {
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{})
	root := fixtureRoot(t)

	p1, err := s.platformFor(context.Background(), root)
	if err != nil {
		t.Fatalf("platformFor #1: %v", err)
	}

	// Force the session to rebuild: Invalidate marks the index stale so the
	// next Index() call allocates a new instance, simulating a real rebuild.
	sess := s.sessionFor(root)
	sess.Invalidate()
	p2, err := s.platformFor(context.Background(), root)
	if err != nil {
		t.Fatalf("platformFor #2: %v", err)
	}
	if p1 == p2 {
		t.Fatal("platformFor did not rebuild after index instance changed — stale graph risk")
	}
}
