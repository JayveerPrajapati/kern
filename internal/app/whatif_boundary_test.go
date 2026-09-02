package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// boundaryFixture writes a tiny two-package Go module: package web (main)
// calls into package db. It returns the fixture root.
func boundaryFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module boundaryfix\n\ngo 1.20\n",
		"db/db.go":    "package db\n\nfunc Ping() {}\n",
		"web/main.go": "package main\n\nimport \"boundaryfix/db\"\n\nfunc Serve() { db.Ping() }\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// writeBoundaries writes .kern/boundaries.json into a fixture root.
func writeBoundaries(t *testing.T, root, content string) {
	t.Helper()
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .kern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "boundaries.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write boundaries.json: %v", err)
	}
}

// TestWhatIfBoundaryViolations asserts the what-if impact surfaces architecture
// boundary violations from .kern/boundaries.json against the affected files:
// with a forbid rule the affected file violates, ArchitectureViolations gains
// a "boundary:" entry; with NO boundaries.json it stays fail-open (no boundary
// entries, no panic).
func TestWhatIfBoundaryViolations(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}

	// With a forbid rule web -> db, removing db.Ping affects web/main.go
	// (Serve calls Ping), which crosses the forbidden boundary.
	root := boundaryFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	p, err := NewWithIndex(root, ix)
	if err != nil {
		t.Fatalf("NewWithIndex: %v", err)
	}
	writeBoundaries(t, root, `{"rules":[{"from":"web","to":"db","action":"forbid"}]}`)

	imp, _, err := p.WhatIf(whatif.RemoveSymbol, "db.Ping", "")
	if err != nil {
		t.Fatalf("WhatIf: %v", err)
	}
	found := false
	for _, v := range imp.ArchitectureViolations {
		if strings.HasPrefix(v, "boundary:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a 'boundary:' entry in ArchitectureViolations, got %v", imp.ArchitectureViolations)
	}

	// Without boundaries.json: fail-open — the dimension holds only what the
	// firewall produced (nothing here), never boundary entries.
	root2 := boundaryFixture(t)
	ix2, err := index.Build(root2)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	p2, err := NewWithIndex(root2, ix2)
	if err != nil {
		t.Fatalf("NewWithIndex: %v", err)
	}
	imp2, _, err := p2.WhatIf(whatif.RemoveSymbol, "db.Ping", "")
	if err != nil {
		t.Fatalf("WhatIf (no boundaries): %v", err)
	}
	for _, v := range imp2.ArchitectureViolations {
		if strings.HasPrefix(v, "boundary:") {
			t.Fatalf("without boundaries.json there must be no boundary entries; got %v", imp2.ArchitectureViolations)
		}
	}
}
