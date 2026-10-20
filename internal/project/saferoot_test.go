package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// A globally wired agent started in $HOME must not make the MCP server index
// (or watch) the user's whole machine.
func TestSessionRefusesAutoIndexOfHome(t *testing.T) {
	t.Setenv("KERN_ALLOW_BROAD_INDEX", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "scratch.go"), []byte("package s\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(home, "")
	defer s.Close()
	if s.watcher != nil {
		t.Error("no file watcher may be started on a broad root")
	}
	_, err := s.Index()
	var ue *index.UnsafeRootError
	if !errors.As(err, &ue) {
		t.Fatalf("expected UnsafeRootError from Session.Index on $HOME, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".kern")); statErr == nil {
		t.Error("a refused first build must not create .kern")
	}
}

func TestSessionStillIndexesAnOrdinaryProject(t *testing.T) {
	t.Setenv("KERN_ALLOW_BROAD_INDEX", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	proj := filepath.Join(home, "code", "app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module example.com/app\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "a.go"), []byte("package app\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(proj, "")
	defer s.Close()
	ix, err := s.Index()
	if err != nil || ix == nil || len(ix.Symbols) == 0 {
		t.Fatalf("an ordinary project must still index on first use: ix=%v err=%v", ix != nil, err)
	}
}
