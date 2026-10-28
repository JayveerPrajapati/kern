package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRebuildInvalidatesPersistedStaleObservation pins the live discrepancy
// found 2026-10-27: a stale verdict persisted before a rebuild kept being
// served by every later process (the observation predates the index it was
// compared against), so `kern doctor` reported a freshly rebuilt, strict-fresh
// index as stale. An observation older than the index generation must not be
// seeded.
func TestRebuildInvalidatesPersistedStaleObservation(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	if err := os.WriteFile(a, []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := fi.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Edit, then take a stale verdict: this persists a WasFresh=false
	// observation for the pre-rebuild index.
	if err := os.WriteFile(a, []byte("package a\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p := before.FreshnessProof(dir); p.Verdict != FreshnessStale {
		t.Fatalf("pre-rebuild verdict = %s, want stale", p.Verdict)
	}

	// Rebuild: the index now matches the tree again.
	fi2, err := Build(dir)
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if err := fi2.Save(); err != nil {
		t.Fatalf("save rebuild: %v", err)
	}
	after, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after rebuild: %v", err)
	}
	if p := after.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("post-rebuild verdict = %s, want fresh — a pre-rebuild stale observation was re-served", p.Verdict)
	}
}
