package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// repoRoot returns the repository root (the directory containing go.mod),
// found by walking up from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := ""
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root (go.mod) not found walking up from %s", dir)
		}
		dir = parent
	}
	return root
}

// TestG35_CatalogDriftRealRepo runs the catalog:drift guard against the real
// repository: the live MCP catalog (this package registers the provider in
// init) must exactly match the tool set declared in the opencode plugin asset
// (internal/setup/assets/plugin/kern.ts). This is the same invariant as
// TestPluginMatchesMCPCatalog, exercised through the diff-gate check.
func TestG35_CatalogDriftRealRepo(t *testing.T) {
	root := repoRoot(t)
	pluginPath := filepath.Join(root, "internal", "setup", "assets", "plugin", "kern.ts")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Skipf("plugin asset not present: %v", err)
	}
	chk := diffgate.NewCatalogDriftCheck(root)
	res, err := chk.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status == domain.StatusSkip {
		t.Skip("catalog provider not registered")
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (MCP catalog and plugin must agree); findings: %+v", res.Status, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(res.Findings))
	}
}
