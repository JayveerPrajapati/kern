package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildStatsPersistedRoundTrip proves the build-cost surface end to
// end: Build records BuildStats, Save persists it through the store, Load
// restores it, StatusReport surfaces it, and Update re-records the pass as
// an incremental "update" with its own reuse count.
func TestBuildStatsPersistedRoundTrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if ix.BuildStats.Kind != "build" {
		t.Fatalf("BuildStats.Kind = %q, want build", ix.BuildStats.Kind)
	}
	if ix.BuildStats.Workers < 1 {
		t.Fatalf("BuildStats.Workers = %d, want >= 1", ix.BuildStats.Workers)
	}
	if ix.BuildStats.Reused != ix.ReusedResults() {
		t.Fatalf("BuildStats.Reused = %d, want %d", ix.BuildStats.Reused, ix.ReusedResults())
	}
	if ix.BuildStats.Ms < 0 {
		t.Fatalf("BuildStats.Ms = %d, want >= 0", ix.BuildStats.Ms)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded == nil {
		t.Fatalf("Load: %v, %v", loaded, err)
	}
	if loaded.BuildStats != ix.BuildStats {
		t.Fatalf("loaded BuildStats %+v, want %+v", loaded.BuildStats, ix.BuildStats)
	}
	st, err := StatusReport(root, false)
	if err != nil || st == nil {
		t.Fatalf("StatusReport: %v, %v", st, err)
	}
	if st.Build != ix.BuildStats {
		t.Fatalf("status Build %+v, want %+v", st.Build, ix.BuildStats)
	}

	// An incremental Update re-records the pass as "update" with its own
	// reuse count (here: one unchanged... no — a.go changed, so Reused is
	// whatever Update actually reused; only the invariants are pinned).
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("package main\n\nfunc hello() {}\nfunc world() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	upd, err := Update(root, loaded)
	if err != nil {
		t.Fatal(err)
	}
	if upd.BuildStats.Kind != "update" {
		t.Fatalf("update BuildStats.Kind = %q, want update", upd.BuildStats.Kind)
	}
	if upd.BuildStats.Workers < 1 {
		t.Fatalf("update BuildStats.Workers = %d, want >= 1", upd.BuildStats.Workers)
	}
	if upd.BuildStats.Reused != upd.ReusedResults() {
		t.Fatalf("update BuildStats.Reused = %d, want %d", upd.BuildStats.Reused, upd.ReusedResults())
	}
}
