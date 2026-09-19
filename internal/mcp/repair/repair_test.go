package repair

import (
	"context"
	"strings"
	"testing"
)

func TestRepairEmptyCompilerOutput(t *testing.T) {
	ctx := context.Background()
	_, err := Repair(ctx, map[string]any{
		"compiler_output": "",
	})
	if err == nil || !strings.Contains(err.Error(), "compiler_output is required") {
		t.Fatalf("expected error on empty compiler_output, got: %v", err)
	}
}

func TestRepairNoDiagnostics(t *testing.T) {
	ctx := context.Background()
	res, err := Repair(ctx, map[string]any{
		"compiler_output": "All tests passed cleanly.",
	})
	if err != nil {
		t.Fatalf("Repair failed: %v", err)
	}
	if !strings.Contains(res, "No auto-repairable compiler diagnostics detected") {
		t.Errorf("expected no diagnostics message, got: %s", res)
	}
}
