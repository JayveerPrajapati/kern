package refactor

import (
	"context"
	"strings"
	"testing"
)

func TestTransactionEmptyEdits(t *testing.T) {
	ctx := context.Background()
	_, err := Transaction(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "edits parameter is required") {
		t.Fatalf("expected error on empty edits, got: %v", err)
	}
}

func TestTransactionEscapingPath(t *testing.T) {
	ctx := context.Background()
	_, err := Transaction(ctx, Hooks{}, map[string]any{
		"edits": `[{"path": "../../outside.go", "content": "package main"}]`,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the project root") {
		t.Fatalf("expected path escaping error, got: %v", err)
	}
}
