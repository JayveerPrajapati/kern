package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTaintViaMCP verifies kern_taint end-to-end: sec.Scan finds a
// sql-injection sink, the built index resolves the entry-point path to it
// (tainted: yes), and generate=true appends a go test scaffold.
func TestTaintViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	app := filepath.Join(root, "app.go")
	src := `package main

import (
	"database/sql"
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/x", H)
}

func H(w http.ResponseWriter, r *http.Request) {
	lookup(r.URL.Query().Get("name"))
}

func lookup(name string) {
	var db *sql.DB
	_ = db.Query(fmt.Sprintf("SELECT * FROM users WHERE name = %s", name))
}
`
	if err := os.WriteFile(app, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := serveOne(t, toolsCallJSON(t, 60, "kern_taint", map[string]any{"root": root}))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "sql-injection") {
		t.Fatalf("expected sql-injection finding, got %q", out)
	}
	if !strings.Contains(out, "tainted: yes") {
		t.Fatalf("expected 'tainted: yes', got %q", out)
	}
	if !strings.Contains(out, "via H") {
		t.Fatalf("expected entry-point attribution 'via H', got %q", out)
	}

	// generate=true appends the deterministic test scaffold.
	resp2 := serveOne(t, toolsCallJSON(t, 61, "kern_taint", map[string]any{"root": root, "generate": true}))
	out2, isErr2 := toolResultText(t, resp2)
	if isErr2 {
		t.Fatalf("unexpected error: %s", out2)
	}
	if !strings.Contains(out2, "TestTaintSQLInjection") {
		t.Fatalf("expected scaffold func TestTaintSQLInjection, got %q", out2)
	}
	if !strings.Contains(out2, "write to:") {
		t.Fatalf("expected write-to line, got %q", out2)
	}
	if !strings.Contains(out2, "```go") {
		t.Fatalf("expected fenced go block, got %q", out2)
	}

	// A file filter that matches nothing yields the empty verdict.
	resp3 := serveOne(t, toolsCallJSON(t, 62, "kern_taint", map[string]any{"root": root, "file": "nope.go"}))
	out3, isErr3 := toolResultText(t, resp3)
	if isErr3 {
		t.Fatalf("unexpected error: %s", out3)
	}
	if !strings.Contains(out3, "no security findings") {
		t.Fatalf("expected 'no security findings' for empty filter, got %q", out3)
	}
}

// TestTaintCleanProjectViaMCP verifies the clean-project verdict: no
// findings -> "no security findings".
func TestTaintCleanProjectViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp := serveOne(t, toolsCallJSON(t, 63, "kern_taint", map[string]any{"root": root}))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "no security findings") {
		t.Fatalf("expected 'no security findings', got %q", out)
	}
}

// TestTaintPythonViaMCP verifies kern_taint end-to-end on a Python sink:
// ScanPythonFile finds py-os-system, the source-file heuristic taints it, and
// generate=true appends a pytest scaffold (G-4).
func TestTaintPythonViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	app := filepath.Join(root, "app.py")
	src := `import os

def run(cmd):
    cmd = req.Body["cmd"]
    os.system(cmd)
`
	if err := os.WriteFile(app, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := serveOne(t, toolsCallJSON(t, 64, "kern_taint", map[string]any{"root": root, "generate": true}))
	out, isErr := toolResultText(t, resp)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	for _, want := range []string{"py-os-system", "tainted: yes", "```python", "def test_py_os_system_5", "write to:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in kern_taint output, got %q", want, out)
		}
	}
}

// TestTaintInvalidRangeViaMCP verifies that a malformed --range/range value
// is rejected with a clear error (G-4).
func TestTaintInvalidRangeViaMCP(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testRoot(t)
	resp := serveOne(t, toolsCallJSON(t, 65, "kern_taint", map[string]any{"root": root, "range": "abc"}))
	out, isErr := toolResultText(t, resp)
	if !isErr {
		t.Fatalf("expected error for invalid range, got %q", out)
	}
	if !strings.Contains(out, "invalid range") {
		t.Fatalf("error should mention 'invalid range', got %q", out)
	}
}
