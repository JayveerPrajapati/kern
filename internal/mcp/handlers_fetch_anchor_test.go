package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/optimize"
)

func TestHandleFetchRawAnchor(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	ctx := context.Background()

	// 1. Missing anchor_id returns error
	_, err := s.handleFetchAnchor(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "anchor_id is required") {
		t.Fatalf("expected anchor_id is required error, got: %v", err)
	}

	// 2. Store an anchor and fetch it via the MCP handler
	raw := "2024-01-01 ERROR critical database deadlock occurred\nstack frame 1\nstack frame 2"
	id := optimize.StoreAnchor(raw)

	res, err := s.handleFetchAnchor(ctx, map[string]any{
		"anchor_id": id,
	})
	if err != nil {
		t.Fatalf("handleFetchAnchor failed: %v", err)
	}
	if res != raw {
		t.Fatalf("hydrated content mismatch:\ngot: %q\nwant: %q", res, raw)
	}
}
