package graph_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/graph"
)

func TestGraph_EmptyBoundaries(t *testing.T) {
	dir := t.TempDir()
	g, err := graph.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(g.Rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(g.Rules))
	}

	mermaid := g.ToMermaid()
	if !strings.Contains(mermaid, "flowchart LR") {
		t.Errorf("expected flowchart header in mermaid output")
	}

	dot := g.ToDOT()
	if !strings.Contains(dot, "digraph ArchitecturalBoundaries") {
		t.Errorf("expected digraph header in DOT output")
	}
}

func TestGraph_WithRules(t *testing.T) {
	dir := t.TempDir()
	kernDir := filepath.Join(dir, ".kern")
	if err := os.MkdirAll(kernDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `{
		"rules": [
			{"from": "frontend", "to": "db", "action": "forbid", "reason": "UI cannot talk directly to DB"},
			{"from": "frontend", "to": "api", "action": "allow"}
		]
	}`
	if err := os.WriteFile(filepath.Join(kernDir, "boundaries.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := graph.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(g.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(g.Rules))
	}

	if len(g.Nodes) != 3 {
		t.Fatalf("expected 3 unique nodes, got %d", len(g.Nodes))
	}

	mermaid := g.ToMermaid()
	if !strings.Contains(mermaid, "frontend") || !strings.Contains(mermaid, "db") || !strings.Contains(mermaid, "api") {
		t.Errorf("mermaid output missing node names: %s", mermaid)
	}
	if !strings.Contains(mermaid, "❌ forbid") {
		t.Errorf("mermaid output missing forbid label: %s", mermaid)
	}
	if !strings.Contains(mermaid, "✓ allow") {
		t.Errorf("mermaid output missing allow label: %s", mermaid)
	}

	dot := g.ToDOT()
	if !strings.Contains(dot, "\"frontend\" -> \"db\"") {
		t.Errorf("DOT output missing edge: %s", dot)
	}

	jsonStr, err := g.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON error: %v", err)
	}
	if !strings.Contains(jsonStr, "\"frontend\"") {
		t.Errorf("JSON output missing content: %s", jsonStr)
	}
}
