package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestCapabilityRegistryDiscovery(t *testing.T) {
	reg := NewCapabilityRegistry()
	all := reg.All()
	if len(all) < 10 {
		t.Fatalf("registry has %d capabilities, want >= 10", len(all))
	}
	// Purpose field populated (P6.6)
	for _, c := range all {
		if strings.TrimSpace(c.Purpose) == "" {
			t.Errorf("capability %q has no purpose (P6.6)", c.Name)
		}
		// Inputs + Dependencies must be populated (P6.6 capability object:
		// name, purpose, inputs, dependencies, tools, permissions, risk,
		// outputs, artifacts). An empty Inputs/Dependencies catalog would not be
		// self-describing for capability planning.
		if len(c.Inputs) == 0 {
			t.Errorf("capability %q has no inputs (P6.6)", c.Name)
		}
		if len(c.Dependencies) == 0 {
			t.Errorf("capability %q has no dependencies (P6.6)", c.Name)
		}
		if len(c.Tools) == 0 {
			t.Errorf("capability %q has no tools (P6.6)", c.Name)
		}
		if strings.TrimSpace(c.Risk) == "" {
			t.Errorf("capability %q has no risk (P6.6)", c.Name)
		}
	}
	// Tools discovery (P6.9)
	tools := reg.Tools()
	if len(tools) == 0 {
		t.Error("no tools discovered")
	}
	if !containsStr(tools, "kern_analyze") {
		t.Errorf("kern_analyze not discovered; got %v", tools)
	}
}

func TestCapabilityRegistryGet(t *testing.T) {
	reg := NewCapabilityRegistry()
	if c, ok := reg.Get("plan"); !ok || c.Name != "plan" {
		t.Errorf("Get(plan) = %+v, %v", c, ok)
	}
	if _, ok := reg.Get("nope"); ok {
		t.Error("Get(nope) should be false")
	}
}

func TestCapabilityPrecheck(t *testing.T) {
	reg := NewCapabilityRegistry()
	toolset := map[string]bool{"kern_execute": true}

	// Missing identity + missing scope -> problems
	p := reg.CapabilityPrecheck("execute", "", "", "dev", toolset)
	if len(p) < 2 {
		t.Errorf("precheck problems = %v, want >=2 (missing id + scope)", p)
	}
	// Missing required tool -> problem
	p2 := reg.CapabilityPrecheck("execute", "agent-1", "svc/x", "dev", map[string]bool{})
	foundTool := false
	for _, s := range p2 {
		if strings.Contains(s, "tool unavailable") {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("precheck missing-tool problem not found: %v", p2)
	}
	// High-risk (deploy) missing env -> problem
	p3 := reg.CapabilityPrecheck("deploy", "agent-1", "svc/x", "", map[string]bool{"kern_execute": true})
	foundEnv := false
	for _, s := range p3 {
		if strings.Contains(s, "environment") {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("deploy precheck missing env problem not found: %v", p3)
	}
	// Unknown capability
	if p4 := reg.CapabilityPrecheck("ghost", "a", "s", "e", toolset); len(p4) == 0 {
		t.Error("unknown capability should produce a problem")
	}
}

func TestFallbackFor(t *testing.T) {
	if got := FallbackFor("kern_what_if"); got != "kern_impact" {
		t.Errorf("FallbackFor(kern_what_if) = %q, want kern_impact", got)
	}
	if got := FallbackFor("kern_analyze"); got != "" {
		t.Errorf("FallbackFor(kern_analyze) = %q, want empty", got)
	}
}

func TestDeterministicPlan(t *testing.T) {
	intent := domain.CompiledIntent{Type: domain.IntentCodeChange, Objective: "add a login endpoint", Target: "auth.go", Scope: "repository"}
	plan := DeterministicPlan(intent)
	for _, want := range []string{"Objective", "Risk", "Scope", "Implementation Steps", "analyze"} {
		if !strings.Contains(strings.ToLower(plan), strings.ToLower(want)) {
			t.Errorf("plan missing %q:\n%s", want, plan)
		}
	}
}

func TestToolDecisionTraceRecorder(t *testing.T) {
	rec := NewToolDecisionTraceRecorder()
	if rec.Len() != 0 {
		t.Error("new recorder should be empty")
	}
	rec.Record(domain.ToolDecisionTrace{Tool: "kern_analyze", WhySelected: "context"})
	rec.Record(domain.ToolDecisionTrace{Tool: "kern_plan", WhySelected: "plan"})
	if rec.Len() != 2 {
		t.Errorf("Len = %d, want 2", rec.Len())
	}
	tr := rec.Traces()
	if len(tr) != 2 || tr[0].Tool != "kern_analyze" || tr[1].Tool != "kern_plan" {
		t.Errorf("Traces = %+v, want ordered [analyze, plan]", tr)
	}
	// Mutating the returned slice must not affect the recorder.
	tr[0] = domain.ToolDecisionTrace{}
	if rec.Traces()[0].Tool != "kern_analyze" {
		t.Error("Traces() should return a defensive copy")
	}
}

func containsStr(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}