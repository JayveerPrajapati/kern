package memory

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryAddEmptyLesson(t *testing.T) {
	ctx := context.Background()
	_, err := Add(ctx, Hooks{}, map[string]any{
		"lesson": "",
	})
	if err == nil || !strings.Contains(err.Error(), "lesson is required") {
		t.Fatalf("expected error on empty lesson, got: %v", err)
	}
}

func TestMemoryRecallEmptyPrompt(t *testing.T) {
	ctx := context.Background()
	_, err := Recall(ctx, Hooks{}, map[string]any{
		"prompt": "",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected error on empty prompt, got: %v", err)
	}
}

func TestMemoryRankedEmptyPrompt(t *testing.T) {
	ctx := context.Background()
	_, err := Ranked(ctx, map[string]any{
		"prompt": "",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt' is required") {
		t.Fatalf("expected error on empty prompt, got: %v", err)
	}
}
