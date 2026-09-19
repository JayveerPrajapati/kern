package stream

import (
	"context"
	"strings"
	"testing"
)

func TestHandle(t *testing.T) {
	ctx := context.Background()

	// 1. Status
	resStatus, err := Handle(ctx, "stdio", map[string]any{"action": "status"})
	if err != nil {
		t.Fatalf("Handle status failed: %v", err)
	}
	if !strings.Contains(resStatus, "Streaming & Progress Transport Status") {
		t.Errorf("expected header in status, got: %s", resStatus)
	}

	// 2. Chunking
	resChunk, err := Handle(ctx, "stdio", map[string]any{
		"action":     "chunk",
		"payload":    "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"chunk_size": 10,
	})
	if err != nil {
		t.Fatalf("Handle chunk failed: %v", err)
	}
	if !strings.Contains(resChunk, "sent 4 chunks (36 chars, chunk size 10)") {
		t.Errorf("expected compact chunk summary, got: %s", resChunk)
	}

	resChunkJSON, err := Handle(ctx, "stdio", map[string]any{
		"action":     "chunk",
		"payload":    "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
		"chunk_size": 10,
		"format":     "json",
	})
	if err != nil {
		t.Fatalf("Handle chunk json failed: %v", err)
	}
	if !strings.Contains(resChunkJSON, "total_chunks") {
		t.Errorf("expected total_chunks in json response, got: %s", resChunkJSON)
	}

	// 3. Channels
	resChannels, err := Handle(ctx, "stdio", map[string]any{"action": "channels"})
	if err != nil {
		t.Fatalf("Handle channels failed: %v", err)
	}
	if !strings.Contains(resChannels, "Registered Streaming Channels") {
		t.Errorf("expected channels header, got: %s", resChannels)
	}

	// 4. Emit
	resEmit, err := Handle(ctx, "stdio", map[string]any{
		"action":         "emit",
		"channel":        "tool_progress",
		"progress_token": "token-1",
		"message":        "processing",
		"percent":        50,
	})
	if err != nil {
		t.Fatalf("Handle emit failed: %v", err)
	}
	if !strings.Contains(resEmit, "Stream event published") {
		t.Errorf("expected emit confirmation, got: %s", resEmit)
	}
}
