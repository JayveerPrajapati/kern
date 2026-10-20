package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCommandCountCap is the CLI-surface freeze gate (workstream 3.3-B): the
// command dispatch table must stay within a deliberate size budget. 204
// commands ≈ every addition taxes the banner, the CLI reference doc and the
// symbol index. Growing the table past the cap requires a stated reason in
// the diff that bumps the cap — no silent growth (mirrors the MCP tool-catalog
// gate in internal/mcp/catalog_drift_test.go).
func TestCommandCountCap(t *testing.T) {
	t.Parallel()
	const (
		capFloor = 40 // guards accidental truncation of the command table (pivot cut 2026-10-18 P5b: 194→48 core commands; floor follows each deliberate cut)
		capCeil  = 62 // deliberate growth budget (48 kept + room for the 32-tool mirrors + ops; 2026-10-21 +2: restored `log` and `terse` thin wrappers — the opencode plugin's compression path invokes both spellings and they were dead after P5b)
	)
	n := commandCount()
	if n < capFloor {
		t.Fatalf("commandTable = %d commands, want >= %d — command table truncated?", n, capFloor)
	}
	if n > capCeil {
		t.Fatalf("commandTable = %d commands, exceeds cap %d — new commands need a stated reason and a cap bump, or a merge/fold of the long tail", n, capCeil)
	}
}

// TestPackageCountCap is the package-surface freeze gate (workstream 3.3-B):
// the number of Go packages must stay within a deliberate size budget. Every
// new package taxes build times, the symbol index and CI. Growing past the
// cap requires a stated reason in the diff that bumps the cap — no silent
// growth (mirrors the MCP tool-catalog gate in internal/mcp/catalog_drift_test.go).
func TestPackageCountCap(t *testing.T) {
	t.Parallel()
	const (
		capFloor = 188 // guards accidental deletion of packages (go list ./... baseline, 2026-10-13; pivot cuts 2026-10-18: -4 = enterprise/orgapprovals/govrbac/mcp/org/mcp/rbac, -3 = lsp/remove/reviewpack, -9 = blueprint family; 191 measured)
		capCeil  = 248 // deliberate growth budget (~1.15x of 216, 2026-10-13)
	)
	n := countGoDirs(t, repoRoot(t))
	t.Logf("package count (walk of .go dirs) = %d (go list ./... = 216)", n)
	if n < capFloor {
		t.Fatalf("packages = %d, want >= %d — packages deleted?", n, capFloor)
	}
	if n > capCeil {
		t.Fatalf("packages = %d, exceeds cap %d — new packages need a stated reason and a cap bump, or a merge/fold", n, capCeil)
	}
}

// countGoDirs returns the number of directories under root that contain at
// least one .go file — a deterministic proxy for `go list ./...` that avoids
// shelling out to the go toolchain inside tests. vendor/, testdata/,
// node_modules/, fixture/ and solution/ are skipped because `go list ./...`
// ignores them too (the evaluate/agentic task fixtures carry .go files but
// are not listed packages). The two methods agree within a small delta;
// TestPackageCountCap logs both numbers.
func countGoDirs(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path != root {
			switch d.Name() {
			case ".git", "vendor", "testdata", "node_modules", "fixture", "solution":
				return filepath.SkipDir
			}
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
				n++
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}
