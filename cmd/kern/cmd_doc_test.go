package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDocSearchHybrid(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	docContent := "# System Architecture Guide\n\nThis guide describes the engine dispatch mechanism and overview.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "arch.md"), []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}

	goCode := `package main

// DispatchEngine processes incoming operations.
func DispatchEngine() string {
	return "dispatched"
}

func main() {
	_ = DispatchEngine()
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(goCode), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Query matching only code
	outCode := captureStdout(t, func() {
		runDocSearch([]string{"DispatchEngine", "--root", root})
	})
	if !strings.Contains(outCode, "## Code") || !strings.Contains(outCode, "DispatchEngine") {
		t.Errorf("expected ## Code section with DispatchEngine, got:\n%s", outCode)
	}

	// 2. Query matching both doc and code ("dispatch")
	outHybrid := captureStdout(t, func() {
		runDocSearch([]string{"dispatch", "--root", root})
	})
	if !strings.Contains(outHybrid, "## Documentation") || !strings.Contains(outHybrid, "## Code") {
		t.Errorf("expected ## Documentation and ## Code sections, got:\n%s", outHybrid)
	}
	if !strings.Contains(outHybrid, "arch.md") || !strings.Contains(outHybrid, "DispatchEngine") {
		t.Errorf("expected arch.md and DispatchEngine in hybrid output, got:\n%s", outHybrid)
	}

	// 3. Query matching neither: a no-match is an ERROR (exit 1, L18 —
	// aligned with `kern search`'s no-match contract), with the reason on
	// stderr instead of a confident-miss success.
	stderr, code := captureStderrExit(t, func() {
		runDocSearch([]string{"nonexistentfoobardispatch9999", "--root", root})
	})
	if code != 1 {
		t.Fatalf("expected exitError{1} on no-match, got exit code %d", code)
	}
	if !strings.Contains(stderr, "no matching document fragments") {
		t.Errorf("expected 'no matching document fragments' on stderr, got:\n%s", stderr)
	}
}

// TestRunDocSearchEmptyIndexExplained pins N3: a repo with no indexed docs
// gets the index explanation instead of a bare "no matching document
// fragments" that reads as a confident miss.
func TestRunDocSearchEmptyIndexExplained(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir() // no docs tree at all
	stderr, code := captureStderrExit(t, func() {
		runDocSearch([]string{"getting started", "--root", root})
	})
	if code != 1 {
		t.Fatalf("expected exitError{1} on no-match, got exit code %d", code)
	}
	if !strings.Contains(stderr, "no matching document fragments") {
		t.Fatalf("expected the no-matching prefix, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "this repo has no documentation indexed") {
		t.Fatalf("expected the empty-index explanation, got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "kern docs index") {
		t.Fatalf("expected the index hint in the explanation, got:\n%s", stderr)
	}
}

// TestRunDocSearchNoMatchExplainsFragmentCount pins N3: a repo with an
// indexed docs tree whose query matches nothing gets the N-fragments variant
// instead of a bare no-match.
func TestRunDocSearchNoMatchExplainsFragmentCount(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	docContent := "# System Architecture Guide\n\nThis guide describes the engine dispatch mechanism and overview.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "arch.md"), []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// First run builds + persists the doc index (the hit is irrelevant to the
	// assertion; the index must exist for the no-match variant to fire).
	_ = captureStdout(t, func() {
		runDocSearch([]string{"dispatch", "--root", root})
	})
	// Second run: query matches nothing → the N-fragments variant, exit 1.
	stderr, code := captureStderrExit(t, func() {
		runDocSearch([]string{"nonexistentfoobardispatch9999", "--root", root})
	})
	if code != 1 {
		t.Fatalf("expected exitError{1} on no-match, got exit code %d", code)
	}
	if !strings.Contains(stderr, "no matching document fragments (query matched nothing in") || !strings.Contains(stderr, "indexed fragments)") {
		t.Fatalf("expected the N-fragments no-match variant, got:\n%s", stderr)
	}
}
