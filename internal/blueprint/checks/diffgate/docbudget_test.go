package diffgate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/docbudget"
)

func writeBudgetManifest(t *testing.T, root string, rules []docbudget.Rule) {
	t.Helper()
	data, err := json.Marshal(docbudget.Manifest{Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc-budgets.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestG39_DocBudgetPass(t *testing.T) {
	root := t.TempDir()
	writeBudgetManifest(t, root, []docbudget.Rule{{Path: "AGENTS.md", MaxWords: 100}})
	writeFile(t, root, "AGENTS.md", "a b c")
	c := NewDocBudgetCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("under-budget doc should PASS, got %s", res.Status)
	}
}

func TestG39_DocBudgetOverLimit(t *testing.T) {
	root := t.TempDir()
	writeBudgetManifest(t, root, []docbudget.Rule{{Path: "AGENTS.md", MaxWords: 3}})
	writeFile(t, root, "AGENTS.md", "a b c d e")
	c := NewDocBudgetCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("over-budget doc should BLOCK, got %s", res.Status)
	}
}

func TestG39_DocBudgetMissingDoc(t *testing.T) {
	root := t.TempDir()
	writeBudgetManifest(t, root, []docbudget.Rule{{Path: "docs/missing.md", MaxWords: 10}})
	c := NewDocBudgetCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("missing listed doc should BLOCK, got %s", res.Status)
	}
}

func TestG39_DocBudgetNoManifest(t *testing.T) {
	root := t.TempDir()
	c := NewDocBudgetCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("no manifest should PASS, got %s", res.Status)
	}
}