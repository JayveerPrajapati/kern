package verification

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// TestDiscoverModuleRoot pins the src/-style layout: a repo root without
// go.mod but with exactly one immediate module child resolves to that child;
// an ambiguous layout stays unresolved rather than guessing.
func TestDiscoverModuleRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "go.mod"), []byte("module example.com/m\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stray.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := fsutil.DiscoverGoModuleRoot(root, nil), filepath.Join(root, "src"); got != want {
		t.Fatalf("DiscoverGoModuleRoot = %q, want %q", got, want)
	}

	if err := os.MkdirAll(filepath.Join(root, "backend"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "backend", "go.mod"), []byte("module example.com/b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fsutil.DiscoverGoModuleRoot(root, nil); got != "" {
		t.Fatalf("ambiguous layout resolved to %q, want unresolved", got)
	}
}
