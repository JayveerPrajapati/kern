package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveRefusesToClobberNewerSchema pins the save-time schema guard: a
// process holding an older-schema index in memory (a long-lived daemon or
// watcher started before an upgrade) must not silently overwrite a newer
// index.json on disk. The load-time version check cannot protect the file
// once it has been loaded, so Save itself refuses.
func TestSaveRefusesToClobberNewerSchema(t *testing.T) {
	root := t.TempDir()
	old := New(root)
	old.Version = 12
	if err := old.Save(); err != nil {
		t.Fatal(err)
	}

	// A newer-schema index may overwrite the older one (the upgrade path).
	upgraded := New(root)
	upgraded.Version = 13
	if err := upgraded.Save(); err != nil {
		t.Fatalf("newer index failed to overwrite older: %v", err)
	}

	// The older in-memory index must now refuse to clobber the newer file.
	err := old.Save()
	if err == nil {
		t.Fatal("older-schema Save succeeded over a newer index.json, want refusal")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite index schema v13") {
		t.Fatalf("guard error = %q, want schema-refusal message", err)
	}

	// The on-disk file must still be the newer one.
	got := onDiskVersion(StorePath(root))
	if got != 13 {
		t.Fatalf("on-disk version after refused save = %d, want 13 (file untouched)", got)
	}
}

// TestOnDiskVersion reads the version field without a full decode.
func TestOnDiskVersion(t *testing.T) {
	if got := onDiskVersion(filepath.Join(t.TempDir(), "absent.json")); got != 0 {
		t.Fatalf("onDiskVersion(absent) = %d, want 0", got)
	}
	root := t.TempDir()
	ix := New(root)
	ix.Version = 12
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	if got := onDiskVersion(StorePath(root)); got != 12 {
		t.Fatalf("onDiskVersion = %d, want 12", got)
	}
	// Garbage file: guard must no-op (0), not fail.
	bad := filepath.Join(t.TempDir(), "index.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := onDiskVersion(bad); got != 0 {
		t.Fatalf("onDiskVersion(garbage) = %d, want 0", got)
	}
}
