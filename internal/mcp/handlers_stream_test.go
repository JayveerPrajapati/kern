package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleStream(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// 1. Status
	resStatus, err := srv.handleStream(ctx, map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("handleStream status failed: %v", err)
	}
	if !strings.Contains(resStatus, "Streaming & Progress Transport Status") {
		t.Errorf("expected header in status, got: %s", resStatus)
	}

	// 2. Chunking
	resChunk, err := srv.handleStream(ctx, map[string]any{
		"action":     "chunk",
		"payload":    "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"chunk_size": 10,
	})
	if err != nil {
		t.Fatalf("handleStream chunk failed: %v", err)
	}
	if !strings.Contains(resChunk, "total_chunks") {
		t.Errorf("expected total_chunks in json response, got: %s", resChunk)
	}
}
