package lsp

import (
	"context"
	"strings"
	"testing"
)

func TestLSPBridgeServers(t *testing.T) {
	ctx := context.Background()
	res, err := Handle(ctx, map[string]any{
		"action": "servers",
	})
	if err != nil {
		t.Fatalf("Handle failed: %v", err)
	}
	if !strings.Contains(res, "LSP Bridge: SERVERS") {
		t.Errorf("expected header in output, got: %s", res)
	}
}
