package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphCmd_Empty(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	code := runGraph([]string{"--repo", dir})
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}

func TestGraphCmd_MermaidAndDot(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	kernDir := filepath.Join(dir, ".kern")
	if err := os.MkdirAll(kernDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `{
		"rules": [
			{"from": "web", "to": "db", "action": "forbid"},
			{"from": "web", "to": "api", "action": "allow"}
		]
	}`
	if err := os.WriteFile(filepath.Join(kernDir, "boundaries.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	outMermaid := filepath.Join(dir, "graph.mmd")
	code := runGraph([]string{"--repo", dir, "--format", "mermaid", "--output", outMermaid})
	if code != 0 {
		t.Fatalf("expected exit code 0 for mermaid, got %d", code)
	}

	b, err := os.ReadFile(outMermaid)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "web") || !strings.Contains(string(b), "db") {
		t.Errorf("mermaid output missing nodes: %s", string(b))
	}

	outDot := filepath.Join(dir, "graph.dot")
	code = runGraph([]string{"--repo", dir, "--format", "dot", "--output", outDot})
	if code != 0 {
		t.Fatalf("expected exit code 0 for dot, got %d", code)
	}

	bDot, err := os.ReadFile(outDot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bDot), "digraph") {
		t.Errorf("dot output missing digraph: %s", string(bDot))
	}
}

func TestGraphCmd_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	code := runGraph([]string{"--repo", dir, "--format", "invalid"})
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid format, got %d", code)
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
}
