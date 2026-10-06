package governance

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestFreshnessMatchesProofNotWallClock pins A3 (deep-dive 2026-10-03): the
// authorize-context proof's IndexFreshness must come from the same
// FreshnessProof machinery every other tool trusts (git tree-OID compare
// with content-hash fallback), not a 5-minute wall-clock heuristic. An
// unchanged tree read an hour after the build must report "fresh" here —
// previously it said "stale" while every other tool said "fresh".
func TestFreshnessMatchesProofNotWallClock(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "main.go")
	if err := os.WriteFile(main, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module f\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init")

	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Old build, unchanged tree: the proof must say fresh even though
	// UpdatedAt is an hour behind the old 5-minute wall-clock cutoff.
	old := ix
	old.UpdatedAt = time.Now().Add(-time.Hour)
	if got := freshness(old, dir); got != "fresh" {
		t.Fatalf("unchanged tree with 1h-old UpdatedAt: freshness = %q, want fresh (A3)", got)
	}

	// Changed tree: stale — regardless of how recent UpdatedAt is.
	if err := os.WriteFile(main, []byte("package main\n\nfunc main() {}\n\nfunc added() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := freshness(ix, dir); got != "stale" {
		t.Fatalf("changed tree with fresh UpdatedAt: freshness = %q, want stale", got)
	}
}
