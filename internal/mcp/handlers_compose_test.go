package mcp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/compose"
)

func TestHandleComposePipelineSuccess(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)
	root := t.TempDir()
	seedTestProject(t, root)
	s.roots = []string{root}

	// Test a multi-step pipeline: health -> search -> health
	pipeline := []map[string]any{
		{
			"tool": "kern_health",
			"args": map[string]any{"root": s.roots[0]},
			"bind": "health_status",
		},
		{
			"tool": "kern_search",
			"args": map[string]any{"query": "Server", "root": s.roots[0]},
			"bind": "search_result",
		},
	}

	res, err := s.handleCompose(context.Background(), map[string]any{
		"pipeline": pipeline,
	})
	if err != nil {
		t.Fatalf("handleCompose error: %v", err)
	}

	if !strings.Contains(res, "=== Step 1: kern_health") {
		t.Errorf("missing step 1 in output: %s", res)
	}
	if !strings.Contains(res, "=== Step 2: kern_search") {
		t.Errorf("missing step 2 in output: %s", res)
	}
	if !strings.Contains(res, "[bound to $health_status]") {
		t.Errorf("missing binding indicator for step 1: %s", res)
	}
	if !strings.Contains(res, "[kern_compose: 2/2 steps completed") {
		t.Errorf("missing completion footer: %s", res)
	}
}

func TestHandleComposeInterpolation(t *testing.T) {
	t.Parallel()
	bindings := map[string]string{
		"$sym":   "LoadUser",
		"target": "auth.go",
	}

	args := map[string]any{
		"exact_var": "$sym",
		"embed_var": "Refactor $sym in target",
		"count":     10,
	}

	resolved := compose.InterpolateArgs(args, bindings)
	if resolved["exact_var"] != "LoadUser" {
		t.Errorf("exact_var = %v, want 'LoadUser'", resolved["exact_var"])
	}
	if resolved["embed_var"] != "Refactor LoadUser in target" {
		t.Errorf("embed_var = %v, want 'Refactor LoadUser in target'", resolved["embed_var"])
	}
	if resolved["count"] != 10 {
		t.Errorf("count = %v, want 10", resolved["count"])
	}
}

func TestHandleComposeRecursivePrevention(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)
	pipeline := []map[string]any{
		{
			"tool": "kern_compose",
			"args": map[string]any{},
		},
	}
	_, err := s.handleCompose(context.Background(), map[string]any{"pipeline": pipeline})
	if err == nil {
		t.Fatal("expected error on recursive kern_compose, got nil")
	}
	if !strings.Contains(err.Error(), "recursive") {
		t.Errorf("expected 'recursive' error message, got: %v", err)
	}
}

func TestHandleComposeStopOnError(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)
	pipeline := []map[string]any{
		{
			"tool": "non_existent_tool_xyz",
			"args": map[string]any{},
		},
		{
			"tool": "kern_health",
			"args": map[string]any{},
		},
	}
	_, err := s.handleCompose(context.Background(), map[string]any{"pipeline": pipeline})
	if err == nil {
		t.Fatal("expected pipeline to fail on unknown tool")
	}
}
