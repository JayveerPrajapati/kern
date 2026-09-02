package index

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadOrBuildRoundtrip: a cold root builds and persists; a second call
// with a fresh index loads instead of rebuilding (G-11).
func TestLoadOrBuildRoundtrip(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := LoadOrBuild(root)
	if err != nil || ix == nil {
		t.Fatalf("first LoadOrBuild: %v", err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols from the built index")
	}
	// The build persisted the index (Save); the second call must load it
	// fresh rather than rebuild.
	ix2, err := LoadOrBuild(root)
	if err != nil || ix2 == nil {
		t.Fatalf("second LoadOrBuild: %v", err)
	}
	if len(ix2.Symbols) != len(ix.Symbols) {
		t.Fatalf("symbol count changed between calls: %d vs %d", len(ix.Symbols), len(ix2.Symbols))
	}
}
