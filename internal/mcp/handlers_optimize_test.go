package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestHandleOptimizeDispatch pins the kern_optimize action dispatcher: the
// action argument is required and unknown actions are rejected up front.
func TestHandleOptimizeDispatch(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), io.Discard)

	if _, err := s.handleOptimize(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "'action' is required") {
		t.Fatalf("expected action-required error, got %v", err)
	}
	if _, err := s.handleOptimize(context.Background(), map[string]any{"action": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown-action error, got %v", err)
	}
}
