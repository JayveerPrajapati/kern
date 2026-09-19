package optimize

import (
	"context"
	"strings"
	"testing"
)

func TestPromptEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Prompt(ctx, map[string]any{
		"prompt": "",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("expected error on empty prompt, got: %v", err)
	}
}

func TestSwapEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Swap(ctx, map[string]any{
		"text": "",
	})
	if err == nil || !strings.Contains(err.Error(), "text is required") {
		t.Fatalf("expected error on empty text, got: %v", err)
	}
}

func TestLogEmpty(t *testing.T) {
	ctx := context.Background()
	_, err := Log(ctx, map[string]any{
		"log": "",
	})
	if err == nil || !strings.Contains(err.Error(), "log is required") {
		t.Fatalf("expected error on empty log, got: %v", err)
	}
}

func TestContextBudget(t *testing.T) {
	ctx := context.Background()
	res, err := ContextBudget(ctx, map[string]any{
		"text": "package main\n\nfunc main() {}\n",
	})
	if err != nil {
		t.Fatalf("ContextBudget failed: %v", err)
	}
	if !strings.Contains(res, "tokens") {
		t.Errorf("expected token count in report, got: %s", res)
	}
}
