package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	t.Parallel()
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

func TestNormalizePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{".", "."},
		{"a/b/c", "a/b/c"},
		{"a\\b\\c", "a/b/c"},
		{"a/b/../c", "a/c"},
		{"a\\b\\..\\c", "a/c"},
		{"./a/b/c", "a/b/c"},
		{".\\a\\b\\c", "a/b/c"},
		{"/a/b/c", "/a/b/c"},
		{"\\a\\b\\c", "/a/b/c"},
	}
	for _, tc := range cases {
		got := NormalizePath(tc.input)
		if got != tc.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNormalizeRelPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{".", "."},
		{"./", "."},
		{"./a/b", "a/b"},
		{".\\a\\b", "a/b"},
		{"a/b/c", "a/b/c"},
		{"a\\b\\c", "a/b/c"},
		{"./a/b/../c", "a/c"},
	}
	for _, tc := range cases {
		got := NormalizeRelPath(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeRelPath(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
