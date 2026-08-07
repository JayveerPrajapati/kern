package project

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSessionResolvesEmptyRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s := New("", "s1")
	if s.Root != cwd {
		t.Fatalf("expected cwd, got %q", s.Root)
	}
	if s.Session != "s1" {
		t.Fatalf("expected session kept, got %q", s.Session)
	}
}

func TestSessionIndexBuildAndStaleRebuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\n// Greet says hello.\nfunc Greet() {}\n")
	s := New(root, "")

	ix, err := s.Index()
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols in first build")
	}
	first := ix

	// Fresh cache must be reused (same pointer).
	again, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatal("expected cached index reused while fresh")
	}

	// Adding a source file makes the cached index stale; Index must rebuild.
	writeFile(t, root, "extra.go", "package main\nfunc Extra() {}\n")
	s.Invalidate()
	rebuilt, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sym := range rebuilt.Symbols {
		if sym.Name == "Extra" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected rebuilt index to include Extra")
	}
}

func TestSessionRecordBestEffort(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := New(t.TempDir(), "rec-test")
	// Must not panic even though no optimization happened.
	s.Record("run_build", "test", "", 100, 40)
	if s.Recorder() == nil {
		t.Fatal("expected recorder with writable cache")
	}
}
