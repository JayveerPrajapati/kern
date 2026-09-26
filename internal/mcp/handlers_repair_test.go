package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairDiagnosticsViaMCP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("test")
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	// 1. Preview repair via MCP
	res, err := s.handleRepairDiagnostics(context.Background(), map[string]any{
		"root":            root,
		"compiler_output": "main.go:4:2: imported and not used: \"os\"",
		"apply":           "false",
	})
	if err != nil {
		t.Fatalf("handleRepairDiagnostics preview failed: %v", err)
	}
	if !strings.Contains(res, "Previewed") || !strings.Contains(res, "Repaired") {
		t.Errorf("expected Previewed and Repaired in result, got:\n%s", res)
	}

	// 2. Apply repair via MCP
	res, err = s.handleRepairDiagnostics(context.Background(), map[string]any{
		"root":            root,
		"compiler_output": "main.go:4:2: imported and not used: \"os\"",
		"apply":           "true",
	})
	if err != nil {
		t.Fatalf("handleRepairDiagnostics apply failed: %v", err)
	}
	if !strings.Contains(res, "Applied") {
		t.Errorf("expected Applied in result, got:\n%s", res)
	}

	// Verify file on disk was modified to remove unused import "os"
	data, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"os"`) {
		t.Errorf("expected import os removed from disk file, got:\n%s", string(data))
	}
}
