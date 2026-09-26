package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleLSPBridge_ServersList(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	res, err := s.handleLSPBridge(context.Background(), map[string]any{
		"action": "servers",
	})
	if err != nil {
		t.Fatalf("handleLSPBridge: %v", err)
	}

	if !strings.Contains(res, "LSP Bridge: SERVERS") {
		t.Errorf("expected header in output, got: %s", res)
	}
}

func TestHandleLSPBridge_NoServerFallback(t *testing.T) {
	t.Parallel()
	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	res, err := s.handleLSPBridge(context.Background(), map[string]any{
		"file":   "foo.unknown_ext_xyz",
		"action": "definition",
	})
	if err != nil {
		t.Fatalf("handleLSPBridge: %v", err)
	}

	if !strings.Contains(res, "Warning:") {
		t.Errorf("expected warning when no server installed, got: %s", res)
	}
}
