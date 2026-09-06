package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHandleSemanticDiff(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{kernRepoRoot}

	// Test 1: Compare HEAD against working tree (or HEAD~1..HEAD)
	res, err := s.handleSemanticDiff(context.Background(), map[string]any{
		"root": kernRepoRoot,
	})
	if err != nil {
		t.Fatalf("handleSemanticDiff error: %v", err)
	}

	if !strings.Contains(res, "SEMANTIC DIFF:") {
		t.Errorf("missing header in response: %s", res)
	}
	if !strings.Contains(res, "Files changed:") {
		t.Errorf("missing summary metrics in response: %s", res)
	}

	// Test 2: Range comparison
	resRange, err := s.handleSemanticDiff(context.Background(), map[string]any{
		"root":  kernRepoRoot,
		"range": "HEAD~1..HEAD",
	})
	if err != nil {
		t.Fatalf("handleSemanticDiff range error: %v", err)
	}
	if !strings.Contains(resRange, "SEMANTIC DIFF:") {
		t.Errorf("missing header in range response: %s", resRange)
	}
}
