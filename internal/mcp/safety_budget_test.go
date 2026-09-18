package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// compactArgs are the args for the cheap read-only tool used throughout these
// tests. It executes against the package dir (server.go exists there).
var compactArgs = map[string]any{"path": "server.go"}

func TestSafetyBudgetDeniesWhenExceeded(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	budget := &domain.SafetyBudget{MaxToolCalls: 1}
	s.WithToolGateway(governance.NewToolGateway(nil), budget)

	// Call 1: under budget → executes normally.
	out, err := s.runTool(context.Background(), "t", "kern_compact_file", compactArgs)
	if err != nil {
		t.Fatalf("under-budget call must not error, got: %v", err)
	}
	if out == "" {
		t.Fatal("under-budget call produced an empty result")
	}
	if budget.ToolCallsUsed() != 1 {
		t.Fatalf("under-budget call must be tracked, ToolCallsUsed=%d", budget.ToolCallsUsed())
	}

	// Call 2: budget exceeded (1 >= 1) → denied before dispatch.
	_, err = s.runTool(context.Background(), "t", "kern_compact_file", compactArgs)
	if err == nil {
		t.Fatal("budget-exceeded call must be denied")
	}
	if !strings.Contains(err.Error(), "safety budget exceeded") || !strings.Contains(err.Error(), "tool call denied") {
		t.Fatalf("denial must carry the structured budget error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "max_tool_calls") {
		t.Fatalf("denial should surface the budget reason, got: %v", err)
	}
	// The denied call must not consume additional budget.
	if budget.ToolCallsUsed() != 1 {
		t.Fatalf("denied call must not be tracked, ToolCallsUsed=%d", budget.ToolCallsUsed())
	}
}

// TestSafetyBudgetUnderBudgetPasses verifies the budget does not interfere
// with calls while it has headroom.
func TestSafetyBudgetUnderBudgetPasses(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	s.WithToolGateway(governance.NewToolGateway(nil), &domain.SafetyBudget{MaxToolCalls: 5})

	for i := 0; i < 3; i++ {
		out, err := s.runTool(context.Background(), "t", "kern_compact_file", compactArgs)
		if err != nil {
			t.Fatalf("call %d must pass under budget, got: %v", i+1, err)
		}
		if out == "" {
			t.Fatalf("call %d produced an empty result", i+1)
		}
	}
}

// TestSafetyBudgetNilGatewayNoop verifies the back-compat contract: a server
// whose gateway is explicitly disabled (nil) behaves exactly as before — no
// budget accounting, no denials, even past what a wired budget would allow.
func TestSafetyBudgetNilGatewayNoop(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	s.WithToolGateway(nil, nil) // explicit opt-out: no-op mode
	if s.gateway != nil || s.budget != nil {
		t.Fatal("WithToolGateway(nil, nil) must clear both gateway and budget")
	}

	for i := 0; i < 3; i++ {
		out, err := s.runTool(context.Background(), "t", "kern_compact_file", compactArgs)
		if err != nil {
			t.Fatalf("nil-gateway call %d must behave as before, got: %v", i+1, err)
		}
		if out == "" {
			t.Fatalf("nil-gateway call %d produced an empty result", i+1)
		}
	}
}

func TestSafetyBudgetWiredOnlyWhenEnvOptIn(t *testing.T) {
	budgetVars := []string{
		"KERN_SAFETY_BUDGET_MAX_TOOL_CALLS", "KERN_SAFETY_BUDGET_MAX_FILES",
		"KERN_SAFETY_BUDGET_MAX_TOKENS", "KERN_SAFETY_BUDGET_MAX_EXTERNAL_CALLS",
		"KERN_SAFETY_BUDGET_MAX_COST", "KERN_SAFETY_BUDGET_MAX_RUNTIME_SECONDS",
		"KERN_SAFETY_BUDGET_MAX_RISK",
	}
	t.Run("no_env_not_wired", func(t *testing.T) {
		for _, n := range budgetVars {
			t.Setenv(n, "") // empty == unset for the opt-in check
		}
		s := NewServer(strings.NewReader(""), &bytes.Buffer{})
		if s.gateway != nil || s.budget != nil {
			t.Fatal("NewServer must not wire the safety budget without KERN_SAFETY_BUDGET_* opt-in")
		}
	})
	t.Run("env_wired", func(t *testing.T) {
		t.Setenv("KERN_SAFETY_BUDGET_MAX_TOOL_CALLS", "2")
		s := NewServer(strings.NewReader(""), &bytes.Buffer{})
		if s.gateway == nil {
			t.Fatal("NewServer must wire the safety-budget ToolGateway when KERN_SAFETY_BUDGET_* is set")
		}
		if s.budget == nil || s.budget.MaxToolCalls != 2 {
			t.Fatalf("env-configured budget must be wired, got %+v", s.budget)
		}
	})
}

// TestSafetyBudgetEnvOverride verifies KERN_SAFETY_BUDGET_MAX_TOOL_CALLS tunes
// the construction-time budget; a malformed value must NOT widen the limit
// (it falls back to the conservative default).
func TestSafetyBudgetEnvOverride(t *testing.T) {
	t.Run("honors_override", func(t *testing.T) {
		t.Setenv("KERN_SAFETY_BUDGET_MAX_TOOL_CALLS", "2")
		s := NewServer(strings.NewReader(""), &bytes.Buffer{})
		s.roots = []string{"/"}
		if s.budget.MaxToolCalls != 2 {
			t.Fatalf("KERN_SAFETY_BUDGET_MAX_TOOL_CALLS=2 not honored, got %d", s.budget.MaxToolCalls)
		}
		// Two under-budget calls succeed, the third is denied.
		for i := 0; i < 2; i++ {
			if _, err := s.runTool(context.Background(), "t", "kern_compact_file", compactArgs); err != nil {
				t.Fatalf("call %d must pass under the env cap, got: %v", i+1, err)
			}
		}
		if _, err := s.runTool(context.Background(), "t", "kern_compact_file", compactArgs); err == nil {
			t.Fatal("third call must be denied when the env budget caps at 2")
		}
	})
	t.Run("malformed_falls_back_to_default", func(t *testing.T) {
		t.Setenv("KERN_SAFETY_BUDGET_MAX_TOOL_CALLS", "not-a-number")
		s := NewServer(strings.NewReader(""), &bytes.Buffer{})
		if s.budget.MaxToolCalls != domain.DefaultSafetyBudget().MaxToolCalls {
			t.Fatalf("malformed override must keep the default cap, got %d", s.budget.MaxToolCalls)
		}
	})
}

// TestSafetyBudgetWithBudgetNilDefaults verifies WithToolGateway with a
// non-nil gateway but a nil budget falls back to the conservative default
// rather than disabling enforcement.
func TestSafetyBudgetWithBudgetNilDefaults(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.WithToolGateway(governance.NewToolGateway(nil), nil)
	if s.gateway == nil {
		t.Fatal("gateway must be set")
	}
	if s.budget == nil {
		t.Fatal("a nil budget must default to DefaultSafetyBudget when a gateway is wired")
	}
	if s.budget.MaxToolCalls != domain.DefaultSafetyBudget().MaxToolCalls {
		t.Fatalf("defaulted budget cap = %d, want %d", s.budget.MaxToolCalls, domain.DefaultSafetyBudget().MaxToolCalls)
	}
}

// TestSafetyBudgetPrecheckToolNoTrackingOnDeny verifies at the choke-point
// level (precheckTool) that a denied call neither executes nor consumes
// budget, and that allowlist-denied tools never consume budget either.
func TestSafetyBudgetPrecheckToolNoTrackingOnDeny(t *testing.T) {
	s := NewServer(strings.NewReader(""), &bytes.Buffer{})
	s.roots = []string{"/"}
	s.WithToolGateway(governance.NewToolGateway(nil), &domain.SafetyBudget{MaxToolCalls: 1})

	name, err := s.precheckTool("kern_compact_file", compactArgs)
	if err != nil {
		t.Fatalf("first precheck must pass: %v", err)
	}
	if name != "kern_compact_file" {
		t.Fatalf("precheck remapped name to %q", name)
	}
	if s.budget.ToolCallsUsed() != 1 {
		t.Fatalf("allowed precheck must track one call, got %d", s.budget.ToolCallsUsed())
	}

	if _, err := s.precheckTool("kern_compact_file", compactArgs); err == nil {
		t.Fatal("second precheck must be denied when the budget is exhausted")
	}
	if s.budget.ToolCallsUsed() != 1 {
		t.Fatalf("denied precheck must not consume budget, got %d", s.budget.ToolCallsUsed())
	}

	// An allowlist-denied tool must fail at the allowlist gate BEFORE the
	// budget gate, without consuming budget.
	s.allowlist = []string{"kern_health"}
	if _, err := s.precheckTool("kern_compact_file", compactArgs); err == nil {
		t.Fatal("allowlist-denied tool must fail precheck")
	}
	if s.budget.ToolCallsUsed() != 1 {
		t.Fatalf("allowlist-denied tool must not consume budget, got %d", s.budget.ToolCallsUsed())
	}
}
