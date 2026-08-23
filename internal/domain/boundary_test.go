package domain

import "testing"

func TestSafetyBudgetToolCallKind(t *testing.T) {
	b := &SafetyBudget{MaxToolCallsByKind: map[string]int{"exec": 2, "read": 5}}
	b.TrackToolCallKind("exec")
	b.TrackToolCallKind("exec")
	if ex, why := b.Exceeded(); !ex || why != "max_tool_calls_by_kind exceeded: exec" {
		t.Fatalf("after 2 exec calls, Exceeded = %v, %q; want exceeded for exec", ex, why)
	}
	// read still under its limit; total counter untouched (0) so no total exceed.
	if b.ToolCallsUsed() != 0 {
		t.Fatalf("TrackToolCallKind should not touch the total counter, got %d", b.ToolCallsUsed())
	}
}

func TestSafetyBudgetEnvDimension(t *testing.T) {
	b := &SafetyBudget{AllowedEnvs: []string{"development", "staging"}}
	b.TrackEnv("production")
	if ex, why := b.Exceeded(); !ex || why != "env not allowed: production" {
		t.Fatalf("Exceeded = %v, %q; want env-not-allowed", ex, why)
	}
	b.TrackEnv("staging")
	if ex, _ := b.Exceeded(); ex {
		t.Fatal("allowed env should not exceed the budget")
	}
	// Empty AllowedEnvs means allow-all (backward compatible).
	b2 := &SafetyBudget{}
	b2.TrackEnv("anything")
	if ex, _ := b2.Exceeded(); ex {
		t.Fatal("empty AllowedEnvs should allow all envs")
	}
}

func TestSafetyBudgetReset(t *testing.T) {
	b := &SafetyBudget{MaxToolCalls: 100, MaxToolCallsByKind: map[string]int{"exec": 5}}
	b.TrackToolCall()
	b.TrackToolCall()
	b.TrackToolCallKind("exec")
	b.TrackEnv("production")
	b.Start()
	if b.ToolCallsUsed() == 0 {
		t.Fatal("expected usage before Reset")
	}
	b.Reset()
	if b.ToolCallsUsed() != 0 {
		t.Fatalf("ToolCallsUsed after Reset = %d, want 0", b.ToolCallsUsed())
	}
	if ex, _ := b.Exceeded(); ex {
		t.Fatal("after Reset nothing should exceed")
	}
	if b.CurrentEnv != "" {
		t.Fatalf("CurrentEnv after Reset = %q, want empty", b.CurrentEnv)
	}
}
