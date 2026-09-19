package note

import (
	"context"
	"strings"
	"testing"
)

func TestNoteUnknownAction(t *testing.T) {
	ctx := context.Background()
	_, err := Handle(ctx, map[string]any{"action": "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown action error, got: %v", err)
	}
}

func TestNoteNewValidation(t *testing.T) {
	ctx := context.Background()
	_, err := Handle(ctx, map[string]any{"action": "new"})
	if err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("expected title is required error, got: %v", err)
	}
}
