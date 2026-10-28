package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeStore(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".kern", "index.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkGitRoot(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestDaemonProjectRootStopsAtGitRoot pins P2-6: a stale ancestor .kern store
// (the /private/tmp case) must not capture a scratch repo below it.
func TestDaemonProjectRootStopsAtGitRoot(t *testing.T) {
	parent := t.TempDir()
	writeStore(t, parent)
	repo := filepath.Join(parent, "repo")
	mkGitRoot(t, repo)
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	got, ok := daemonProjectRoot()
	if !ok || got != repo {
		t.Fatalf("daemonProjectRoot = %q (ok=%v), want %q (git root, not the ancestor store)", got, ok, repo)
	}
}

func TestDaemonProjectRootUsesStoreUnderGitRoot(t *testing.T) {
	parent := t.TempDir()
	writeStore(t, parent)
	repo := filepath.Join(parent, "repo")
	mkGitRoot(t, repo)
	writeStore(t, repo)
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	if got, ok := daemonProjectRoot(); !ok || got != repo {
		t.Fatalf("daemonProjectRoot = %q (ok=%v), want %q", got, ok, repo)
	}
}

func TestDaemonProjectRootNoGitIgnoresAncestorStore(t *testing.T) {
	parent := t.TempDir()
	writeStore(t, parent)
	scratch := filepath.Join(parent, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(scratch)
	if got, ok := daemonProjectRoot(); !ok || got != scratch {
		t.Fatalf("daemonProjectRoot = %q (ok=%v), want the cwd %q, never the ancestor store", got, ok, scratch)
	}
}
