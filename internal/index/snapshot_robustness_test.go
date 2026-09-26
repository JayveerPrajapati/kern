package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
// into memory — LoadSnapshot fails loudly. Files are extended sparsely so the
// test allocates nothing. (The loadBinSnapshot over-cap degradation is tested
// under -tags nosqlite in snapcache_test.go, where the snapshot write is live.)
func TestSnapshotOverCapReadErrors(t *testing.T) {
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
}
