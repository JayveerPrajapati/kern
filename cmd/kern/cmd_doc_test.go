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

	// 3. Query matching neither
	outNone := captureStdout(t, func() {
		runDocSearch([]string{"nonexistentfoobardispatch9999", "--root", root})
	})
	if !strings.Contains(outNone, "no matching document fragments") {
		t.Errorf("expected 'no matching document fragments', got:\n%s", outNone)
	}
}
