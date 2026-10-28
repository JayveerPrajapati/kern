package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPersistedFreshnessSnapshotRoundTrip pins P2-6: a non-git root has no
// tree OID, so the content walk must be reusable across processes. The walk is
// persisted; a fresh Load seeds the stat-gate and can prove freshness with
// stats alone, while a real edit still forces the full re-hash and reports
// stale.
func TestPersistedFreshnessSnapshotRoundTrip(t *testing.T) {
	// Exercise the stat-gate directly rather than the in-process 5s proof
	// memo (which intentionally re-serves the last verdict for a few seconds).
	t.Setenv("KERN_FRESHNESS_PROOF_CACHE_MS", "0")
	dir := t.TempDir()
	writeFile := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("package p\n\nfunc A() {}\n")
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if ix.Stale() {
		t.Fatal("freshly built index reported stale")
	}
	if _, err := os.Stat(filepath.Join(dir, ".kern", freshnessSnapshotFile)); err != nil {
		t.Fatalf("freshness walk was not persisted: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil || loaded == nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.retainedSnapshot() == nil {
		t.Fatal("Load did not seed the persisted content-walk observation")
	}
	if p := loaded.FreshnessProof(dir); p.Verdict != FreshnessFresh {
		t.Fatalf("freshness proof = %q, want fresh (stat-gated persisted walk)", p.Verdict)
	}

	// A real edit (different size) invalidates the observation.
	writeFile("package p\n\nfunc A() { println(1) }\n")
	if !loaded.Stale() {
		t.Fatal("edited file reported fresh: the stat-gate did not detect the change")
	}
}
