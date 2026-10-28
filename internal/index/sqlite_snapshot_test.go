package index

import (
	"os"
	"testing"
	"time"
)

// fixtureIndex builds a minimal persistable index rooted at dir.
func fixtureIndex(dir string) *Index {
	ix := &Index{
		Root:          dir,
		Version:       indexVersion,
		UpdatedAt:     time.Now(),
		Symbols:       []Symbol{{Name: "Alpha", Kind: "func", File: "a.go", Line: 1}},
		FileHashes:    map[string]string{"a.go": "h"},
		Calls:         map[string][]CallEdge{},
		Callers:       map[string][]string{},
		Pkgs:          map[string]*Pkg{},
		BuildStats:    BuildStats{Kind: "test", Ms: 1},
		SymbolsByFile: map[string][]Symbol{},
	}
	ix.initMaps()
	return ix
}

// TestSQLiteSnapshotRoundTripAndGenerationInvalidation pins the restored fast
// load and its P2-4 freshness contract: the snapshot is accepted only when its
// header token matches the store generation, never on file mtime. A store
// rewritten without a snapshot rewrite must invalidate the snapshot even if
// the SQLite file's size/mtime happen to match.
func TestSQLiteSnapshotRoundTripAndGenerationInvalidation(t *testing.T) {
	if !SQLiteEnabled() {
		t.Skip("sqlite not compiled in")
	}
	dir := t.TempDir()
	ix := fixtureIndex(dir)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok := loadSQLiteBinSnapshot(dir)
	if !ok || got == nil {
		t.Fatal("loadSQLiteBinSnapshot rejected the snapshot written by Save")
	}
	if len(got.Symbols) != 1 || got.Symbols[0].Name != "Alpha" {
		t.Fatalf("snapshot symbols = %+v", got.Symbols)
	}
	if got.SymbolsByFile != nil {
		t.Fatal("snapshot carried the derived SymbolsByFile map (it must be rebuilt, not encoded)")
	}

	// Rewrite the generation token only. The snapshot header still points at
	// the old token, so the snapshot must be rejected even though the store's
	// file stat is unchanged.
	s, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := s.SetMeta("generation", "different-generation"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSQLiteBinSnapshot(dir); ok {
		t.Fatal("stale snapshot accepted after the store generation changed")
	}
}

// TestLoadSQLiteReadOnlyNoWrites pins read-only loading: with no usable snapshot and a
// read-only store, Load must read the SQLite index without running DDL, WAL
// creation, mkdir, or .git/info/exclude writes. KERN_READONLY forbids the
// read-write fallback.
func TestLoadSQLiteReadOnlyNoWrites(t *testing.T) {
	if !SQLiteEnabled() {
		t.Skip("sqlite not compiled in")
	}
	dir := t.TempDir()
	if err := fixtureIndex(dir).Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Force the SQLite path (no snapshot, no JSON) and fold the WAL into the
	// main database so the fixture models a checkpointed/prebuilt store.
	if err := os.Remove(StorePath(dir)); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Remove(binSnapshotPathFor(StorePath(dir))); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	ck, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if _, err := ck.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if err := ck.Close(); err != nil {
		t.Fatal(err)
	}
	dbPath := SQLitePath(dir)
	_ = os.Remove(dbPath + "-wal")
	_ = os.Remove(dbPath + "-shm")
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(dbPath, 0o644)
	})
	t.Setenv("KERN_INDEX_NO_SNAPSHOT", "1")
	t.Setenv("KERN_READONLY", "1")

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on a read-only store: %v", err)
	}
	if got == nil || len(got.Symbols) != 1 {
		t.Fatalf("Load returned %+v", got)
	}
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		t.Fatal("read-only Load created a WAL file")
	}
}

// TestSQLiteCountsMetadataOnly pins the P2-1 fast counts used by
// `kern index where` / `--status`.
func TestSQLiteCountsMetadataOnly(t *testing.T) {
	if !SQLiteEnabled() {
		t.Skip("sqlite not compiled in")
	}
	dir := t.TempDir()
	if err := fixtureIndex(dir).Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	syms, files, pkgs, err := SQLiteCounts(dir)
	if err != nil {
		t.Fatalf("SQLiteCounts: %v", err)
	}
	if syms != 1 || files != 1 || pkgs != 0 {
		t.Fatalf("SQLiteCounts = (%d,%d,%d), want (1,1,0)", syms, files, pkgs)
	}
}
