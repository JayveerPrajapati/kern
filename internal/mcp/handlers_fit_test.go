package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestFitContextViaMCP(t *testing.T) {
	root := fixtureRoot(t)
	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	// 1. Call kern_fit_context with high budget -> returns full tier
	res, err := s.handleFitContext(context.Background(), map[string]any{
		"root":       root,
		"max_tokens": "10000",
		"files":      "web/server.go,web/handler.go",
	})
	if err != nil {
		t.Fatalf("handleFitContext failed: %v", err)
	}
	if !strings.Contains(res, "full") {
		t.Errorf("expected full tier in response, got:\n%s", res)
	}

	// 2. Call kern_fit_context with format=json
	jsonRes, err := s.handleFitContext(context.Background(), map[string]any{
		"root":       root,
		"max_tokens": "10000",
		"files":      "web/server.go",
		"format":     "json",
	})
	if err != nil {
		t.Fatalf("handleFitContext json failed: %v", err)
	}
	if !strings.Contains(jsonRes, `"tier": "full"`) {
		t.Errorf("expected json response with tier full, got:\n%s", jsonRes)
	}

	// 3. Test NL meta routing
	tool, args, ok := classifyProjectTools("fit context for web server", "fit context for web server")
	if !ok || tool != "kern_fit_context" {
		t.Errorf("expected classifyProjectTools to route to kern_fit_context, got tool=%s, ok=%v, args=%v", tool, ok, args)
	}
}
