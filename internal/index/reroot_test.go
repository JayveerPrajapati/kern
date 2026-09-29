package index

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// copyTree copies a directory tree (used to simulate a repo copied/moved
// together with its .kern — the A1-N1 stale-root scenario).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
	if err != nil {
		t.Fatalf("copyTree %s → %s: %v", src, dst, err)
	}
}

// TestLoadReRootsMovedRepo pins the dogfooding A1-N1 fix: when a repo is
// copied/moved together with its .kern, the persisted index keeps the ORIGINAL
// absolute root. Load must re-point the index's Root at the directory it was
// loaded for, so every subsequent freshness evaluation compares against THIS
// tree — otherwise Stale() evaluated the original (unchanged) tree and
// reported the stale index "fresh" forever, silently reusing it (only
// `kern index --force` healed the copy).
func TestLoadReRootsMovedRepo(t *testing.T) {
	srcA := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcA, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ixA, err := Build(srcA)
	if err != nil {
		t.Fatalf("Build(srcA): %v", err)
	}
	if err := ixA.Save(); err != nil {
		t.Fatalf("Save(srcA): %v", err)
	}

	// Copy the repo INCLUDING .kern to a new location (the stale-root case).
	dstB := t.TempDir()
	copyTree(t, srcA, dstB)

	ixB, err := Load(dstB)
	if err != nil {
		t.Fatalf("Load(dstB): %v", err)
	}
	if ixB == nil {
		t.Fatal("Load(dstB) = nil")
	}
	absB, _ := filepath.Abs(dstB)
	if filepath.Clean(ixB.Root) != filepath.Clean(absB) {
		t.Fatalf("ix.Root = %q, want %q (re-pointed at the loaded root, not the original)", ixB.Root, absB)
	}
	// The loaded copy must resolve symbols against the copy (paths rooted at B).
	if len(ixB.Symbols) == 0 {
		t.Fatal("loaded copy has no symbols")
	}

	// A content change in the copy must make the index STALE, so LoadOrBuild
	// rebuilds and the new symbol is visible — the pre-fix behavior served the
	// stale index (freshness evaluated against the original tree).
	if err := os.WriteFile(filepath.Join(dstB, "b.go"), []byte("package a\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ixR, err := LoadOrBuild(dstB)
	if err != nil {
		t.Fatalf("LoadOrBuild(dstB): %v", err)
	}
	if ixR == nil {
		t.Fatal("LoadOrBuild(dstB) = nil")
	}
	found := false
	for _, s := range ixR.Symbols {
		if s.Name == "B" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("rebuilt index missing new symbol B — a stale index was reused (A1-N1)")
	}
	if filepath.Clean(ixR.Root) != filepath.Clean(absB) {
		t.Fatalf("rebuilt index root = %q, want %q", ixR.Root, absB)
	}
}
