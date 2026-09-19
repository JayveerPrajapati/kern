package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestFillEmptyTemplate(t *testing.T) {
	ctx := context.Background()
	_, err := Fill(ctx, map[string]any{
		"template": "",
	})
	if err == nil || !strings.Contains(err.Error(), "template is required") {
		t.Fatalf("expected error on empty template, got: %v", err)
	}
}

func TestFillInlineTemplate(t *testing.T) {
	ctx := context.Background()
	res, err := Fill(ctx, map[string]any{
		"template": "Hello {{TASK}} in {{FILE}}",
		"task":     "Refactor",
		"file":     "main.go",
	})
	if err != nil {
		t.Fatalf("Fill inline template failed: %v", err)
	}
	if !strings.Contains(res, "Hello Refactor in main.go") {
		t.Errorf("expected rendered text, got: %s", res)
	}
}
