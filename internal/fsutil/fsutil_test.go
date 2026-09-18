package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")

	// Write with an explicit permission; the final file must carry it.
	if err := WriteFileAtomic(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("content = %q, want %q", b, "hello")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %o, want 600", fi.Mode().Perm())
	}

	// Overwrite existing content (the rename must replace it).
	if err := WriteFileAtomic(path, []byte("world"), 0o644); err != nil {
		t.Fatalf("WriteFileAtomic overwrite: %v", err)
	}
	b, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back after overwrite: %v", err)
	}
	if string(b) != "world" {
		t.Fatalf("content = %q, want %q", b, "world")
	}
	fi, err = os.Stat(path)
	if err != nil {
		t.Fatalf("stat after overwrite: %v", err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("perm = %o, want 644", fi.Mode().Perm())
	}

	// No stray temp files may remain in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir has %d entries, want exactly the target file", len(entries))
	}
}
