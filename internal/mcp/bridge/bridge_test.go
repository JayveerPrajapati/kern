package bridge

import (
	"context"
	"strings"
	"testing"
)

func TestCallToolEmptyServer(t *testing.T) {
	ctx := context.Background()
	_, err := CallTool(ctx, map[string]any{
		"server": "",
		"tool":   "echo",
	})
	if err == nil || !strings.Contains(err.Error(), "server is required") {
		t.Fatalf("expected error on empty server, got: %v", err)
	}
}

func TestCallToolEmptyTool(t *testing.T) {
	ctx := context.Background()
	_, err := CallTool(ctx, map[string]any{
		"server": "github",
		"tool":   "",
	})
	if err == nil || !strings.Contains(err.Error(), "tool is required") {
		t.Fatalf("expected error on empty tool, got: %v", err)
	}
}
