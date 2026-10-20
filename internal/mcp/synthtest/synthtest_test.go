package synthtest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestSynthesizeEmptyTarget(t *testing.T) {
	ctx := context.Background()
	_, err := Synthesize(ctx, Hooks{}, map[string]any{
		"target": "",
		"code":   "",
	})
	if err == nil || !strings.Contains(err.Error(), "synthesize test") {
		t.Fatalf("expected error on empty target, got: %v", err)
	}
}

func TestSynthesizeValidCode(t *testing.T) {
	ctx := context.Background()
	code := "package mathutil\n\n// Add returns sum.\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	res, err := Synthesize(ctx, Hooks{}, map[string]any{
		"target": "Add",
		"code":   code,
		"format": "markdown",
	})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if !strings.Contains(res, "# Synthesize Test Report") {
		t.Errorf("expected report header, got: %s", res)
	}
	if !strings.Contains(res, "TestAdd") {
		t.Errorf("expected TestAdd in report, got: %s", res)
	}
}

func TestSynthesizeJSONFormat(t *testing.T) {
	ctx := context.Background()
	code := "package mathutil\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n"
	res, err := Synthesize(ctx, Hooks{}, map[string]any{
		"target": "Sub",
		"code":   code,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if !strings.Contains(res, `"target_symbol": "Sub"`) {
		t.Errorf("expected json target_symbol, got: %s", res)
	}
}

// TestSynthesizeAutoGapIndexLoadError pins the B1 fix: a failing index load
// under --auto-gap must surface as its own error naming the fix — the load
// error used to be swallowed, collapsing "index failed" into the engine's
// generic missing-source message.
func TestSynthesizeAutoGapIndexLoadError(t *testing.T) {
	ctx := context.Background()
	_, err := Synthesize(ctx, Hooks{LoadIndex: func(ctx context.Context, root string) (*index.Index, error) {
		return nil, fmt.Errorf("boom")
	}}, map[string]any{"auto_gap": true})
	if err == nil || !strings.Contains(err.Error(), "failed to load") {
		t.Fatalf("expected index-load error to surface, got: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "kern index") {
		t.Fatalf("error should name the fix (kern index), got: %v", err)
	}
}
