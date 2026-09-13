package diffgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

func writeCatalogDoc(t *testing.T, root string) {
	t.Helper()
	SetCatalogProvider(func() []ToolInfo { return fakeCatalog() })
	catalog, ok := toolCatalog()
	if !ok {
		t.Fatal("catalog provider not set")
	}
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool-catalog.md"), GenerateCatalogDoc(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestG36_CatalogDocFresh(t *testing.T) {
	root := t.TempDir()
	writeCatalogDoc(t, root)
	c := NewCatalogDocCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("fresh catalog doc should PASS, got %s (%s)", res.Status, res.Error)
	}
}

func TestG36_CatalogDocStale(t *testing.T) {
	root := t.TempDir()
	writeCatalogDoc(t, root)
	// Stale: mutate one description line after generation.
	docPath := filepath.Join(root, filepath.FromSlash(catalogDocRelPath))
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte(strings.Replace(string(data), "kern_alpha", "kern_alpha_mutated", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewCatalogDocCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("stale catalog doc should BLOCK, got %s", res.Status)
	}
}

func TestG36_CatalogDocMissing(t *testing.T) {
	root := t.TempDir()
	SetCatalogProvider(func() []ToolInfo { return fakeCatalog() })
	c := NewCatalogDocCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("missing catalog doc should BLOCK, got %s", res.Status)
	}
}

func TestG36_CatalogDocMissingTool(t *testing.T) {
	root := t.TempDir()
	writeCatalogDoc(t, root)
	// Add a new tool to the live catalog after the doc was generated.
	SetCatalogProvider(func() []ToolInfo {
		c := fakeCatalog()
		return append(c, ToolInfo{Name: "kern_newtool", Phase: "meta", RiskLevel: "low"})
	})
	c := NewCatalogDocCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("undocumented tool should BLOCK, got %s", res.Status)
	}
	if !strings.Contains(res.Error, "kern_newtool") {
		t.Errorf("block error should name the missing tool: %s", res.Error)
	}
}

func TestGenerateCatalogDocDeterministic(t *testing.T) {
	SetCatalogProvider(func() []ToolInfo { return fakeCatalog() })
	catalog, _ := toolCatalog()
	a := GenerateCatalogDoc(catalog)
	b := GenerateCatalogDoc(catalog)
	if string(a) != string(b) {
		t.Fatal("GenerateCatalogDoc must be byte-stable for the same catalog")
	}
	if !strings.Contains(string(a), "kern_alpha") {
		t.Error("doc should contain every tool name")
	}
	if !strings.Contains(string(a), "docs never drift") && !strings.Contains(string(a), "do not edit by hand") {
		t.Error("doc should carry the generated-by header")
	}
}