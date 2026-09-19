package compose

import (
	"context"
	"strings"
	"testing"
)

func TestComposeEmptyPipeline(t *testing.T) {
	ctx := context.Background()
	_, err := Compose(ctx, Hooks{}, map[string]any{
		"pipeline": []any{},
	})
	if err == nil || !strings.Contains(err.Error(), "pipeline must contain") {
		t.Fatalf("expected error on empty pipeline, got: %v", err)
	}
}

func TestComposeRecursiveBomb(t *testing.T) {
	ctx := context.Background()
	_, err := Compose(ctx, Hooks{}, map[string]any{
		"pipeline": []PipelineStep{
			{Tool: "kern_compose"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "recursive kern_compose") {
		t.Fatalf("expected error on recursive bomb, got: %v", err)
	}
}

func TestInterpolateValue(t *testing.T) {
	bindings := map[string]string{
		"$foo":   "bar",
		"target": "pkg/main.go",
	}
	res := InterpolateValue("$foo", bindings)
	if res != "bar" {
		t.Errorf("expected bar, got: %v", res)
	}
	resSub := InterpolateValue("file=$target", bindings)
	if resSub != "file=pkg/main.go" {
		t.Errorf("expected file=pkg/main.go, got: %v", resSub)
	}
}
