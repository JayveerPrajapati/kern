package merge

import (
	"context"
	"strings"
	"testing"
)

func TestSemanticMergeMissingInput(t *testing.T) {
	ctx := context.Background()
	_, err := SemanticMerge(ctx, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "base version") {
		t.Fatalf("expected error on missing input, got: %v", err)
	}
}

func TestSemanticMergeSimple(t *testing.T) {
	ctx := context.Background()
	base := "package main\n\nfunc A() int { return 1 }\n"
	local := "package main\n\nfunc A() int { return 1 }\nfunc B() int { return 2 }\n"
	remote := "package main\n\nfunc A() int { return 1 }\nfunc C() int { return 3 }\n"

	res, err := SemanticMerge(ctx, map[string]any{
		"base":   base,
		"local":  local,
		"remote": remote,
	})
	if err != nil {
		t.Fatalf("SemanticMerge failed: %v", err)
	}
	if !strings.Contains(res, "Clean Merge**: `true`") {
		t.Errorf("expected clean merge, got:\n%s", res)
	}
}
