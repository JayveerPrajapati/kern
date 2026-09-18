package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestGraphSnapshotSaveAtomicNoTornFile: GraphSnapshot.Save must write
// atomically (temp + rename) — after Save there is exactly the target file,
// no leftover temp, and a stray temp in the same directory must not confuse
// LoadSnapshot.
func TestGraphSnapshotSaveAtomicNoTornFile(t *testing.T) {
	_, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("whole", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.json")
	if err := snap.Save(path); err != nil {
		t.Fatal(err)
	}
	// No temp files may survive the atomic save.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("atomic Save left a temp file behind: %s", e.Name())
		}
	}
	if _, err := LoadSnapshot(path); err != nil {
		t.Fatalf("LoadSnapshot after atomic Save: %v", err)
	}

	// A stray temp (as if a concurrent Save crashed) must not affect Load.
	tmp, err := os.CreateTemp(dir, ".kern-snap-*.tmp")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tmp.WriteString("torn")
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := LoadSnapshot(path); err != nil {
		t.Fatalf("LoadSnapshot with a stray temp present: %v", err)
	}
}

// TestSnapshotOverCapReadErrors: files over snapshotMaxSize must never be read
// into memory — LoadSnapshot fails loudly, loadBinSnapshot degrades to the
// JSON path (ok=false). Files are extended sparsely so the test allocates
// nothing.
func TestSnapshotOverCapReadErrors(t *testing.T) {
	// (a) GraphSnapshot.LoadSnapshot: over-cap is a loud error.
	_, ix := buildSnapshotIndex(t)
	snap, err := ix.Snapshot("whole", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := snap.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, snapshotMaxSize+1); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("LoadSnapshot must error on an over-cap file, got nil")
	} else if !strings.Contains(err.Error(), "read cap") {
		t.Fatalf("LoadSnapshot over-cap error should name the cap, got: %v", err)
	}

	// (b) loadBinSnapshot: over-cap degrades to ok=false (JSON fallback).
	root, ix2 := buildSnapshotIndex(t)
	if err := ix2.Save(); err != nil {
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
