package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleExplain(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	res, err := srv.handleExplain(ctx, map[string]any{
		"target": "Server",
	})
	if err != nil {
		t.Fatalf("handleExplain failed: %v", err)
	}

	if !strings.Contains(res, "Server") {
		t.Errorf("expected Server in narrative, got: %s", res)
	}
}
