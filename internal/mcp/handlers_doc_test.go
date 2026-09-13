package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleDocSearchHybrid(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	docContent := "# Widget System Guide\n\nThis guide describes the widget configuration and deployment steps.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "guide.md"), []byte(docContent), 0o644); err != nil {
		t.Fatal(err)
	}

	goCode := `package main

// CreateWidget creates a new widget instance.
func CreateWidget() string {
	return "widget"
}

func main() {
	_ = CreateWidget()
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(goCode), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	ctx := context.Background()

	// 1. Query matching only code: should return ## Code section with CreateWidget
	resCode, err := s.handleDocSearch(ctx, map[string]any{"root": root, "query": "CreateWidget"})
	if err != nil {
		t.Fatalf("handleDocSearch code error: %v", err)
	}
	if !strings.Contains(resCode, "## Code") || !strings.Contains(resCode, "CreateWidget") {
		t.Errorf("expected ## Code with CreateWidget, got: %q", resCode)
	}

	// 2. Query matching both doc and code (e.g. "Widget"): should return ## Documentation and ## Code sections
	resHybrid, err := s.handleDocSearch(ctx, map[string]any{"root": root, "query": "Widget"})
	if err != nil {
		t.Fatalf("handleDocSearch hybrid error: %v", err)
	}
	if !strings.Contains(resHybrid, "## Documentation") || !strings.Contains(resHybrid, "## Code") {
		t.Errorf("expected both ## Documentation and ## Code sections, got: %q", resHybrid)
	}
	if !strings.Contains(resHybrid, "guide.md") || !strings.Contains(resHybrid, "CreateWidget") {
		t.Errorf("expected guide.md and CreateWidget in hybrid output, got: %q", resHybrid)
	}

	// 3. Query matching neither: should return "no matching document fragments"
	resNone, err := s.handleDocSearch(ctx, map[string]any{"root": root, "query": "nonexistentfoobardispatch9999"})
	if err != nil {
		t.Fatalf("handleDocSearch none error: %v", err)
	}
	if !strings.Contains(resNone, "no matching document fragments") {
		t.Errorf("expected 'no matching document fragments', got: %q", resNone)
	}
}
