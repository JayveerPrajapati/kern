package index

import (
	"fmt"
	"os"
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
