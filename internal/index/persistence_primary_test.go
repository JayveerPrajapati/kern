//go:build !nosqlite

// SQLite-primary persistence tests (default build): pin that Save writes ONLY
// the SQLite store (no JSON cache, no gob snapshot), that Load reads it back
// identically, that staleness behaves, and that legacy JSON/gob caches still
// load as a migration path and are superseded — never shadowed — by the next
// SQLite-primary write.
package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveIsSQLitePrimaryOnly: a default-build Save must write exactly one
// persisted format — the SQLite store. The JSON cache and gob snapshot are
// the nosqlite/fallback formats and must NOT appear on disk.
func TestSaveIsSQLitePrimaryOnly(t *testing.T) {
	dir := t.TempDir()
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".kern"))
	if err != nil {
		t.Fatalf("read .kern: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name()] = true
	}
	if !names["index.sqlite"] {
		t.Fatalf("index.sqlite missing after Save; .kern has: %v", names)
	}
	if names["index.json"] {
		t.Errorf("index.json written by SQLite-primary Save — the JSON cache is the nosqlite/fallback format")
	}
	if names["index.json.snap"] {
		t.Errorf("index.json.snap written by SQLite-primary Save — the gob snapshot is the nosqlite/fallback format")
	}
}

// TestSaveLoadRoundTripSQLitePrimary: Build → Save → Load must return
// identical data through the SQLite store, and Stale() must behave on the
// loaded index (fresh on an unchanged tree, stale after an edit).
func TestSaveLoadRoundTripSQLitePrimary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n\nfunc Hello() {}\nfunc helper() { Hello() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got == nil {
		t.Fatal("Load returned nil")
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Errorf("symbol count diverged: loaded %d, want %d", len(got.Symbols), len(ix.Symbols))
	}
	if len(got.Calls) != len(ix.Calls) || len(got.FileHashes) != len(ix.FileHashes) {
		t.Errorf("loaded index diverges: %d calls/%d files, want %d calls/%d files",
			len(got.Calls), len(got.FileHashes), len(ix.Calls), len(ix.FileHashes))
	}
	if got.Identity == nil {
		t.Fatal("loaded index lost its build-time Identity (SQLite meta must carry it)")
	}
	// Freshness on the unchanged tree, then stale after an edit.
	if got.Stale() {
		t.Error("Stale() = true on an unchanged tree (SQLite-loaded identity must prove freshness)")
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n\nfunc Hello() {}\nfunc helper() { Hello() }\nfunc added() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !got.Stale() {
		t.Error("Stale() = false after a source edit")
	}
}

// TestLegacyJSONGobCacheLoadsAndIsSuperseded: a legacy cache (index.json +
// index.json.snap, no SQLite) written by an older kern must still load on the
// new code; the first SQLite-primary Save then supersedes it, and Load serves
// the NEW content — the legacy snapshot must never shadow the newer store.
func TestLegacyJSONGobCacheLoadsAndIsSuperseded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Simulate an older kern write: JSON cache + gob snapshot, no SQLite.
	writeJSONFixture(t, legacy)
	if err := writeBinSnapshot(legacy, StorePath(dir)); err != nil {
		t.Fatalf("writeBinSnapshot: %v", err)
	}
	if _, err := os.Stat(SQLitePath(dir)); err == nil {
		t.Fatal("precondition: SQLite store must not exist yet")
	}

	// Migration read: the new code loads the legacy cache (gob preferred).
	migrated, err := Load(dir)
	if err != nil {
		t.Fatalf("Load of legacy cache: %v", err)
	}
	if migrated == nil || len(migrated.Symbols) != len(legacy.Symbols) {
		t.Fatalf("legacy cache did not load: got %d symbols, want %d", len(migrated.Symbols), len(legacy.Symbols))
	}
	if migrated.Stale() {
		t.Error("legacy cache judged stale on an unchanged tree")
	}

	// New build (adds a symbol) + SQLite-primary Save.
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n\nfunc Hello() {}\nfunc Added() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := fresh.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The legacy files are left in place (never deleted), but Load must serve
	// the NEW SQLite content — the frozen snapshot must not shadow it.
	for _, name := range []string{"index.json", "index.json.snap"} {
		if _, err := os.Stat(filepath.Join(dir, ".kern", name)); err != nil {
			t.Errorf("legacy %s was removed by the SQLite-primary write (must be left in place): %v", name, err)
		}
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load after supersede: %v", err)
	}
	if got == nil || len(got.Symbols) != len(fresh.Symbols) {
		t.Fatalf("Load after supersede served stale content: got %d symbols, want %d", len(got.Symbols), len(fresh.Symbols))
	}
	found := false
	for _, s := range got.Symbols {
		if s.Name == "Added" {
			found = true
			break
		}
	}
	if !found {
		t.Error("newly added symbol missing after supersede — Load served the legacy snapshot")
	}
}

// TestLegacyJSONOnlyCacheLoads: a JSON-only legacy cache (no snapshot, no
// SQLite) still loads through the JSON migration path.
func TestLegacyJSONOnlyCacheLoads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package hello\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacy, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	writeJSONFixture(t, legacy)
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load of JSON-only cache: %v", err)
	}
	if got == nil || len(got.Symbols) != len(legacy.Symbols) {
		t.Fatalf("JSON-only cache did not load: got %d symbols, want %d", len(got.Symbols), len(legacy.Symbols))
	}
}
