package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A query must never create its root: `kern docs search <q> <typo>` used to
// build a brand-new ./<typo>/.kern index and report a confident no-match
// (2026-11-10 break campaign).
func TestDocsQueryDoesNotCreateRoot(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "arch.md"), []byte("# System Architecture Guide\n\nThis guide describes the engine dispatch mechanism and overview.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(root, "TypoRoot")
	_, code := captureStderrExit(t, func() {
		runDocs([]string{"search", "dispatch", missing})
	})
	if code != 2 {
		t.Fatalf("nonexistent root: exit = %d, want 2 (usage)", code)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("docs query created its root %s: %v", missing, err)
	}

	// The natural `docs search <q>` form works against a real root.
	out := captureStdout(t, func() {
		runDocs([]string{"search", "dispatch", "--root", root})
	})
	if !strings.Contains(out, "arch.md") {
		t.Fatalf("docs search alias did not query the real root:\n%s", out)
	}
}
