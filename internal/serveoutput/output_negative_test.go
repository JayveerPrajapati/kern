package serveoutput

import (
	"strings"
	"testing"
)

// TestCallOutputBudgetRejectsNegative (audit F7, 2026-11-21): max_output=0 is
// the documented disable value, but a negative budget is a client mistake and
// must fail loud instead of silently disabling the sandbox.
func TestCallOutputBudgetRejectsNegative(t *testing.T) {
	if _, err := CallOutputBudget("kern_explore", map[string]any{"max_output": "-100"}); err == nil || !strings.Contains(err.Error(), "max_output") {
		t.Fatalf("negative max_output must be an error, got: %v", err)
	}
	if _, err := CallOutputBudget("kern_explore", map[string]any{"max_output": "0"}); err != nil {
		t.Fatalf("max_output=0 (disable) must stay valid, got: %v", err)
	}
	if _, err := CallOutputBudget("kern_explore", map[string]any{"max_output": "5000"}); err != nil {
		t.Fatalf("positive max_output must stay valid, got: %v", err)
	}
	if _, err := CallOutputBudget("kern_explore", map[string]any{"max_output": "abc"}); err == nil || !strings.Contains(err.Error(), "invalid integer") {
		t.Fatalf("malformed max_output must stay an error, got: %v", err)
	}
}
