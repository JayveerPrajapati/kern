package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHandlePreEdit(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{kernRepoRoot}

	// Test 1: Query by file
	res, err := s.handlePreEdit(context.Background(), map[string]any{
		"root": kernRepoRoot,
		"file": "internal/index/engine.go",
	})
	if err != nil {
		t.Fatalf("handlePreEdit error: %v", err)
	}
	if !strings.Contains(res, "PRE-EDIT PREDICTIVE IMPACT REPORT") {
		t.Errorf("missing header in response: %s", res)
	}
	if !strings.Contains(res, "Direct Callers") {
		t.Errorf("missing Direct Callers section: %s", res)
	}
	if !strings.Contains(res, "Transitive Blast Radius") {
		t.Errorf("missing Transitive Blast Radius section: %s", res)
	}

	// Test 2: Query by symbol
	resSym, err := s.handlePreEdit(context.Background(), map[string]any{
		"root":   kernRepoRoot,
		"symbol": "NewServer",
	})
	if err != nil {
		t.Fatalf("handlePreEdit symbol error: %v", err)
	}
	if !strings.Contains(resSym, "Target Symbol: NewServer") {
		t.Errorf("missing target symbol line: %s", resSym)
	}

	// Test 3: Missing required arguments
	_, errEmpty := s.handlePreEdit(context.Background(), map[string]any{
		"root": kernRepoRoot,
	})
	if errEmpty == nil {
		t.Error("expected error when both file and symbol are empty")
	}
}
