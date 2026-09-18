package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/checks/diffgate"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
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
// repository: the live MCP catalog must exactly match the tool set declared
// in the opencode plugin asset (internal/setup/assets/plugin/kern.ts). This
// is the same invariant as TestPluginMatchesMCPCatalog, exercised through
// the diff-gate check. The catalog is injected EXPLICITLY at construction
// (the fail-loud wiring the server uses: catalog.WithDiffgateTools →
// diffgate.ToolInfos → NewCatalogDriftCheck(root, tools)) — there is no
// init() registration anywhere, and a missing injection fails loud at the
// constructor (compile error) or at Run (StatusError), never a silent SKIP.
func TestG35_CatalogDriftRealRepo(t *testing.T) {
	root := repoRoot(t)
	pluginPath := filepath.Join(root, "internal", "setup", "assets", "plugin", "kern.ts")
	if _, err := os.Stat(pluginPath); err != nil {
		t.Skipf("plugin asset not present: %v", err)
	}
	catalog.WithDiffgateTools()
	chk := diffgate.NewCatalogDriftCheck(root, diffgate.ToolInfos())
	res, err := chk.Run(context.Background(), domain.ChangeRequest{RepositoryRoot: root})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Status == domain.StatusError {
		t.Fatalf("status = ERROR: %s (catalog must be wired at construction)", res.Error)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("status = %s, want PASS (MCP catalog and plugin must agree); findings: %+v", res.Status, res.Findings)
	}
	if len(res.Findings) != 0 {
		t.Fatalf("findings = %d, want 0", len(res.Findings))
	}
}

func TestNewCatalogDriftCheckRequiresExplicitTools(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow compile-fail proof under -short")
	}
	root := repoRoot(t)
	// -o into a temp dir: without it, a successful build (or one killed
	// mid-link) writes an artifact into the repo root.
	outPath := filepath.Join(t.TempDir(), "compilefail_proof")
	cmd := exec.Command("go", "build", "-tags", "compilefail_proof", "-o", outPath, "./internal/blueprint/checks/diffgate/compilefail_proof/")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOPROXY=off") // hermetic: resolve from the warm module cache only
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("pre-fix one-argument NewCatalogDriftCheck compiled; want compile failure (missing provider must fail at the wiring site)\n%s", out)
	}
	// The failure must be the missing-injection argument count, not
	// something incidental.
	if !strings.Contains(string(out), "not enough arguments") {
		t.Fatalf("compile failure is not the expected missing-injection error:\n%s", out)
	}
}

// TestMCPToolCatalogSizeCap is the catalog freeze gate: the MCP tool catalog
// must stay within a deliberate size budget. 145 tools ≈ 11-15 tool schemas
// per agent context window; every addition taxes every agent session that
// connects to the server. Growing the catalog past the cap requires a stated
// reason in the diff that bumps the cap — no silent growth (mirrors the
// ARCHITECTURE.md LOC-cap drift gate).
func TestMCPToolCatalogSizeCap(t *testing.T) {
	const (
		capFloor = 140 // guards accidental truncation of the catalog
		capCeil  = 160 // deliberate growth budget (~1.1x of 145, 2026-09-17)
	)
	n := len(ToolNames())
	if n < capFloor {
		t.Fatalf("catalog = %d tools, want >= %d — catalog truncated?", n, capFloor)
	}
	if n > capCeil {
		t.Fatalf("catalog = %d tools, exceeds cap %d — new tools need a stated reason and a cap bump, or a merge/fold of the long tail", n, capCeil)
	}
}
