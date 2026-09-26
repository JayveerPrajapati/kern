//go:build nosqlite

// Binary-snapshot write-path tests. The gob snapshot (index.json.snap) is a
// derived cache of the JSON index: Save writes it only on the JSON fallback
// path — the -tags nosqlite build, or a SQLite write failure in the default
// build — so these tests pin the write + freshness wiring exactly where the
// write is live. The READ side of the snapshot (loadBinSnapshot in Load /
// LoadFile, plus the migration and supersede behavior) is covered in every
// build by the SQLite-primary tests in persistence_primary_test.go and the
// legacy-fixture tests here.
package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBinSnapshotRoundTrip: Save writes the binary snapshot alongside the
// JSON, and Load prefers it — the loaded index must be identical to the one
// saved (symbols, calls, files).
func TestBinSnapshotRoundTrip(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	snapPath := binSnapshotPathFor(StorePath(root))
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("binary snapshot not written: %v", err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != ix.Version || len(got.Symbols) != len(ix.Symbols) || len(got.Calls) != len(ix.Calls) || len(got.FileHashes) != len(ix.FileHashes) {
		t.Fatalf("loaded index diverges: got %d symbols/%d calls, want %d/%d", len(got.Symbols), len(got.Calls), len(ix.Symbols), len(ix.Calls))
	}
}

// TestBinSnapshotStaleFallsBackToJSON: rewriting index.json invalidates the
// snapshot; Load must serve the NEW content via the JSON path, never the
// stale snapshot.
func TestBinSnapshotStaleFallsBackToJSON(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Rewrite index.json with different content (touching mtime and size),
	// keeping the REAL schema version so the JSON path loads it cleanly —
	// a stale snapshot (holding the old v13 index with all symbols) must
	// not be served.
	p := StorePath(root)
	orig, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	modified := append([]byte(`{"root":"`), root...)
	modified = append(modified, fmt.Sprintf(`","version":%d,"symbols":[]}`, indexVersion)...)
	if string(modified) == string(orig) {
		t.Fatal("test modification is a no-op")
	}
	if err := os.WriteFile(p, modified, 0o600); err != nil {
		t.Fatal(err)
	}
	// Stale snapshot must not be served: Load returns the modified (empty)
	// symbol set — proving it took the JSON path.
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load after JSON rewrite: %v", err)
	}
	if len(got.Symbols) != 0 {
		t.Fatalf("stale snapshot was served: got %d symbols, want 0 (JSON path)", len(got.Symbols))
	}
}

// TestBinSnapshotCorruptFallsBackToJSON: a corrupt snapshot file degrades to
// the JSON path instead of erroring.
func TestBinSnapshotCorruptFallsBackToJSON(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.WriteFile(binSnapshotPathFor(StorePath(root)), []byte("not a gob snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load with corrupt snapshot: %v", err)
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Fatalf("corrupt snapshot broke load: got %d symbols, want %d", len(got.Symbols), len(ix.Symbols))
	}
}

// TestBinSnapshotMissingFallsBackToJSON: no snapshot file at all (e.g. an
// index written by an older kern binary) is the pre-snapshot behavior.
func TestBinSnapshotMissingFallsBackToJSON(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Remove(binSnapshotPathFor(StorePath(root))); err != nil {
		t.Fatal(err)
	}
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load with no snapshot: %v", err)
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Fatalf("missing snapshot broke load: got %d symbols, want %d", len(got.Symbols), len(ix.Symbols))
	}
}

// TestLoadBinSnapshotFreshness: the freshness gate itself — a matching
// index.json stat yields the snapshot; a touched index.json (mtime bump)
// rejects it.
func TestLoadBinSnapshotFreshness(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	p := StorePath(root)
	if _, ok := loadBinSnapshot(p); !ok {
		t.Fatal("fresh snapshot rejected")
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadBinSnapshot(p); ok {
		t.Fatal("stale snapshot accepted after index.json mtime bump")
	}
}

// TestBinSnapshotLoadFilePath: LoadFile gets the same fast path.
func TestBinSnapshotLoadFilePath(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadFile(StorePath(root))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Fatalf("LoadFile diverged: got %d symbols, want %d", len(got.Symbols), len(ix.Symbols))
	}
}

// TestBinSnapshotTornTempIgnored: a temp file left behind by a crashed
// writeBinSnapshot must never be consumed as the snapshot (loadBinSnapshot
// reads only the final .snap path), and a torn final .snap — partial bytes at
// the real path, as a non-atomic writer would leave — must degrade to ok=false
// so the caller falls back to the JSON path instead of decoding garbage.
func TestBinSnapshotTornTempIgnored(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jsonPath := StorePath(root)
	snapPath := binSnapshotPathFor(jsonPath)

	// (1) Stray temp from a crashed write, sitting next to a complete .snap:
	// the real snapshot must still be served (the temp is never read).
	tmp, err := os.CreateTemp(filepath.Dir(snapPath), ".index.snap.*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmp.Write([]byte("torn partial snapshot bytes")); err != nil {
		t.Fatal(err)
	}
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, ok := loadBinSnapshot(jsonPath); !ok {
		t.Fatal("loadBinSnapshot failed with a stray temp file present; must serve the complete .snap")
	}

	// (2) Torn final .snap (simulates a crash that wrote straight to the
	// final path): must return ok=false, never a partial decode.
	if err := os.WriteFile(snapPath, []byte("torn partial snapshot bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadBinSnapshot(jsonPath); ok {
		t.Fatal("torn .snap was served; loadBinSnapshot must return ok=false and fall back to JSON")
	}
}

// TestLoadBinSnapshotOverCapDegrades: an over-cap snapshot is never read into
// memory; loadBinSnapshot returns ok=false so the caller falls back to JSON.
func TestLoadBinSnapshotOverCapDegrades(t *testing.T) {
	root, ix := buildSnapshotIndex(t)
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	snapPath := binSnapshotPathFor(StorePath(root))
	if err := os.Truncate(snapPath, snapshotMaxSize+1); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadBinSnapshot(StorePath(root)); ok {
		t.Fatal("loadBinSnapshot must return ok=false for an over-cap file")
	}
}
