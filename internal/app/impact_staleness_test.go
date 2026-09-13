package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestImpactStalenessBanner: the impact text output must carry the one-line
// staleness banner when a file the report cites changed since the index was
// built, and nothing when the index is fresh. The banner travels through
// TaskService.Impact, which feeds the CLI (kern impact), the MCP tool
// (kern_impact) and the REST endpoint (/v1/impact).
func TestImpactStalenessBanner(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module staleness\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "app.go"),
		"package staleness\n\n// Greet says hello.\nfunc Greet() string { return \"hi\" }\n")
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// Fresh index: no banner.
	_, _, text, err := ts.Impact("Greet")
	if err != nil {
		t.Fatalf("Impact: %v", err)
	}
	if strings.Contains(text, "changed since index") {
		t.Errorf("fresh impact text carries a staleness banner:\n%s", text)
	}

	// Drift the cited file, then re-run against the same (now stale) index.
	if err := os.WriteFile(filepath.Join(root, "app.go"),
		[]byte("package staleness\n\n// Greet says hello.\nfunc Greet() string { return \"hi!\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, text, err = ts.Impact("Greet")
	if err != nil {
		t.Fatalf("Impact (stale): %v", err)
	}
	if !strings.HasPrefix(text, "⚠️ 1 file(s) changed since index") {
		t.Errorf("stale impact text missing banner; got:\n%s", text)
	}
}
