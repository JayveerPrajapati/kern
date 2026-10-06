package meta

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestHandlePassthroughArgs_CompactFile pins the F3 structured passthrough:
// a path param the request text cannot carry is merged into the routed
// tool's args after classification, so meta->compact_file delivers a real
// result instead of "path is required".
func TestHandlePassthroughArgs_CompactFile(t *testing.T) {
	var gotTool string
	var gotArgs map[string]any
	h := Hooks{
		RouteTool: func(ctx context.Context, name string, args map[string]any) (string, error) {
			gotTool, gotArgs = name, args
			return "compact summary", nil
		},
		CostHint: func(tool string) (int, int) { return 0, 0 },
	}
	out, err := Handle(context.Background(), h, map[string]any{
		"request": "compact this file",
		"args":    map[string]any{"path": "internal/mcp/meta.go"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if gotTool != "kern_compact_file" {
		t.Fatalf("routed to %q, want kern_compact_file", gotTool)
	}
	if p, _ := gotArgs["path"].(string); p != "internal/mcp/meta.go" {
		t.Fatalf("routed args[path] = %q, want internal/mcp/meta.go", p)
	}
	if !strings.Contains(out, "classified as: kern_compact_file") {
		t.Fatalf("output missing classification line: %q", out)
	}
}

// TestHandlePassthroughArgs_ASTSearch pins the same passthrough for pattern:
// meta->ast_search receives the pattern and returns matches instead of
// "pattern is required".
func TestHandlePassthroughArgs_ASTSearch(t *testing.T) {
	var gotTool string
	var gotArgs map[string]any
	h := Hooks{
		RouteTool: func(ctx context.Context, name string, args map[string]any) (string, error) {
			gotTool, gotArgs = name, args
			return "3 matches", nil
		},
		CostHint: func(tool string) (int, int) { return 0, 0 },
	}
	out, err := Handle(context.Background(), h, map[string]any{
		"request": "use kern_ast_search",
		"args":    map[string]any{"pattern": "console.log($MSG)"},
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if gotTool != "kern_ast_search" {
		t.Fatalf("routed to %q, want kern_ast_search", gotTool)
	}
	if p, _ := gotArgs["pattern"].(string); p != "console.log($MSG)" {
		t.Fatalf("routed args[pattern] = %q, want console.log($MSG)", p)
	}
	if !strings.Contains(out, "classified as: kern_ast_search") {
		t.Fatalf("output missing classification line: %q", out)
	}
}

// TestHandleMissingRequiredParamGuidance pins the F3 error contract: when
// the routed tool errors on a missing required param, the error must teach
// the agent how to pass that param through kern_meta's args object.
func TestHandleMissingRequiredParamGuidance(t *testing.T) {
	h := Hooks{
		RouteTool: func(ctx context.Context, name string, args map[string]any) (string, error) {
			return "", errors.New("pattern is required")
		},
		CostHint: func(tool string) (int, int) { return 0, 0 },
	}
	_, err := Handle(context.Background(), h, map[string]any{"request": "use kern_ast_search"})
	if err == nil {
		t.Fatal("Handle must surface the routed error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "pattern is required") {
		t.Fatalf("error lost the routed message: %q", msg)
	}
	if !strings.Contains(msg, `args={"pattern": "..."}`) {
		t.Fatalf("error must show how to pass pattern through meta args, got: %q", msg)
	}
}
