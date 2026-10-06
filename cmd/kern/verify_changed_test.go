package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/verification"
)

// TestChangedTestPackages pins the --changed scoping derivation (shared with
// the MCP kern_verify fast tier via verification.ChangedTestPackages): staged,
// unstaged and untracked .go files map to their package dirs; non-Go files
// and renames-follow-new-path behave; clean tree → nil (default scope).
func TestChangedTestPackages(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.23\n")
	write("a/a.go", "package a\n")
	write("b/b.go", "package b\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")

	// clean tree → default scope (empty means "no changed Go packages")
	if got := verification.ChangedTestPackages(dir); len(got) != 0 {
		t.Fatalf("clean tree: got %v, want empty", got)
	}

	// unstaged modification in a/, staged new file in c/d/, untracked in e/
	write("a/a.go", "package a // modified\n")
	write("c/d/c.go", "package d\n")
	write("e/e.go", "package e\n")
	write("notes.txt", "not go\n")
	git("add", "c")
	want := []string{"./a", "./c/d", "./e"}
	if got := verification.ChangedTestPackages(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("changed: got %v, want %v", got, want)
	}

	// non-Go-only changes → default scope
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// (a/, c/d, e still hold Go changes — the non-Go file alone is ignored)
	if got := verification.ChangedTestPackages(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("non-Go ignored: got %v, want %v", got, want)
	}
}
