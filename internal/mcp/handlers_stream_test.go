package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleStream(t *testing.T) {
	t.Parallel()
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
	// D4: compact text is the default...
	if !strings.Contains(resChunk, "sent 4 chunks (36 chars, chunk size 10)") {
		t.Errorf("expected compact chunk summary, got: %s", resChunk)
	}
	// ...and full JSON stays behind format=json.
	resChunkJSON, err := srv.handleStream(ctx, map[string]any{
		"action":     "chunk",
		"payload":    "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"chunk_size": 10,
		"format":     "json",
	})
	if err != nil {
		t.Fatalf("handleStream chunk json failed: %v", err)
	}
	if !strings.Contains(resChunkJSON, "total_chunks") {
		t.Errorf("expected total_chunks in json response, got: %s", resChunkJSON)
	}
}
