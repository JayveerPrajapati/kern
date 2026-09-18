package diffgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

func writeContractsDoc(t *testing.T, root string) {
	t.Helper()
	SetToolInfos(fakeCatalog())
	catalog := ToolInfos()
	dir := filepath.Join(root, "docs", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool-contracts.md"), GenerateContractsDoc(catalog), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestContractsDocFresh(t *testing.T) {
	root := t.TempDir()
	writeContractsDoc(t, root)
	c := NewContractsDocCheck(root, ToolInfos())
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("fresh contracts doc should PASS, got %s (%s)", res.Status, res.Error)
	}
}

func TestContractsDocStale(t *testing.T) {
	root := t.TempDir()
	writeContractsDoc(t, root)
	// Stale: mutate one tool name after generation.
	docPath := filepath.Join(root, filepath.FromSlash(contractsDocRelPath))
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, []byte(strings.Replace(string(data), "kern_alpha", "kern_alpha_mutated", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewContractsDocCheck(root, ToolInfos())
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("stale contracts doc should BLOCK, got %s", res.Status)
	}
}

func TestContractsDocMissingTool(t *testing.T) {
	root := t.TempDir()
	// Generate the doc from a catalog WITHOUT kern_alpha, so the committed
	// doc never mentions it while the live catalog still does.
	full := fakeCatalog()
	var trimmed []ToolInfo
	for _, t := range full {
		if t.Name != "kern_alpha" {
			trimmed = append(trimmed, t)
		}
	}
	SetToolInfos(full)
	dir := filepath.Join(root, "docs", "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tool-contracts.md"), GenerateContractsDoc(trimmed), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewContractsDocCheck(root, ToolInfos())
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("doc missing a registered tool should BLOCK, got %s", res.Status)
	}
	if !strings.Contains(res.Error, "kern_alpha") {
		t.Errorf("BLOCK error should name the missing tool, got: %s", res.Error)
	}
}

func TestContractsDocMissingFile(t *testing.T) {
	root := t.TempDir()
	SetToolInfos(fakeCatalog())
	c := NewContractsDocCheck(root, ToolInfos())
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("missing contracts doc should BLOCK, got %s", res.Status)
	}
}

// TestGenerateContractsDocCoversEveryToolAndPreservesExamples: the generated
// document must mention every catalog tool and keep the usage-examples
// appendix verbatim.
func TestGenerateContractsDocCoversEveryToolAndPreservesExamples(t *testing.T) {
	catalog := fakeCatalog()
	doc := string(GenerateContractsDoc(catalog))
	for _, tool := range catalog {
		if !strings.Contains(doc, "`"+tool.Name+"`") {
			t.Errorf("generated contracts doc missing tool %s", tool.Name)
		}
	}
	for _, want := range []string{"## Usage examples", "Example 5 — meta: route by intent", "kern_verify_output"} {
		if !strings.Contains(doc, want) {
			t.Errorf("generated contracts doc missing %q", want)
		}
	}
}
