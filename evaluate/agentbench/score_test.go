package agentbench

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkspaceDeltaSplit pins the over-engineering proxy: test code and
// non-test code land in separate buckets.
func TestWorkspaceDeltaSplit(t *testing.T) {
	fix := t.TempDir()
	ws := t.TempDir()
	write := func(base, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(base, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(fix, "a.go", "package a\n")
	write(ws, "a.go", "package a\n")          // unchanged
	write(ws, "normal.go", "package a\n\n")   // added, 2 lines
	write(ws, "x_test.go", "package a\n\n\n") // added, 3 lines (test bucket)
	if err := os.MkdirAll(filepath.Join(ws, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(ws, filepath.Join(".kern", "adoption.log"), "{}\n") // plugin state, not agent work
	if err := os.MkdirAll(filepath.Join(ws, ".densic"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(ws, filepath.Join(".densic", "index.db"), "binary") // index state, not agent work

	d := WorkspaceDelta(fix, ws)
	if d == nil {
		t.Fatal("WorkspaceDelta returned nil")
	}
	if d.FilesChanged != 2 {
		t.Errorf("files_changed = %d, want 2 (generated plugin state excluded)", d.FilesChanged)
	}
	if d.NonTestLOC != 2 {
		t.Errorf("non_test_loc = %d, want 2", d.NonTestLOC)
	}
	if d.TestLOC != 3 {
		t.Errorf("test_loc = %d, want 3", d.TestLOC)
	}
}
