package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestHandlePromptFill(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close() // drain sessions (watcher + background index saves) before t.TempDir cleanup
	root := fixtureRoot(t)
	s.roots = []string{root}

	// Test 1: Render debug template with task description
	res, err := s.handlePromptFill(context.Background(), map[string]any{
		"root":     root,
		"template": "debug",
		"task":     "panic in handleHTTP on bad payload",
		"file":     "web/server.go",
	})
	if err != nil {
		t.Fatalf("handlePromptFill error: %v", err)
	}

	if !strings.Contains(res, "# debug") {
		t.Errorf("missing template title: %s", res)
	}
	if !strings.Contains(res, "panic in handleHTTP on bad payload") {
		t.Errorf("missing task in rendered prompt: %s", res)
	}
	if !strings.Contains(res, "web/server.go") {
		t.Errorf("missing file in rendered prompt: %s", res)
	}

	// Test 2: Custom slots
	resCustom, err := s.handlePromptFill(context.Background(), map[string]any{
		"root":     root,
		"template": "explain",
		"slots": map[string]any{
			"TASK": "Explain the architecture of kern",
		},
	})
	if err != nil {
		t.Fatalf("handlePromptFill with custom slots error: %v", err)
	}
	if !strings.Contains(resCustom, "Explain the architecture of kern") {
		t.Errorf("custom task slot not reflected: %s", resCustom)
	}

	// Test 3: Missing template
	_, errMissing := s.handlePromptFill(context.Background(), map[string]any{
		"root": root,
	})
	if errMissing == nil {
		t.Error("expected error when template argument is empty")
	}

	// Test 4: Inline template string
	resInline, errInline := s.handlePromptFill(context.Background(), map[string]any{
		"root":     root,
		"template": "Hello agent! Please solve: {{TASK}} in file {{FILE}}",
		"task":     "fix nil pointer dereference",
		"file":     "main.go",
	})
	if errInline != nil {
		t.Fatalf("handlePromptFill with inline template error: %v", errInline)
	}
	if !strings.Contains(resInline, "Hello agent! Please solve: fix nil pointer dereference in file main.go") {
		t.Errorf("inline template not properly interpolated: %s", resInline)
	}
}
