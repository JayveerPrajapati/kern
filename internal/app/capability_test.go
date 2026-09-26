package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
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

func TestAssemblePlanNetNewFeature(t *testing.T) {
	ts := &TaskService{}
	pkt := domain.ContextPacket{
		Symbols: []domain.Symbol{{Name: "RandomTestClass"}},
		Files:   []domain.File{{Path: "src/test/RandomTestClass.java"}},
	}
	plan := ts.assemblePlan("Add REST endpoint for consumer lag", pkt)
	if len(plan.AffectedComponents) != 0 {
		t.Errorf("expected 0 affected components for net-new feature, got %v", plan.AffectedComponents)
	}
	if !strings.Contains(plan.Scope, "net-new") {
		t.Errorf("expected net-new in Scope, got %q", plan.Scope)
	}
}

// TestAssemblePlanRenameIntentConcreteSteps verifies that a rename intent
// produces concrete implementation steps (the explicit change kind first,
// then per-symbol steps, then the required-validation steps) instead of one
// vacuous generic step.
func TestAssemblePlanRenameIntentConcreteSteps(t *testing.T) {
	ts := &TaskService{}
	pkt := domain.ContextPacket{
		Symbols:            []domain.Symbol{{Name: "OldName", File: "internal/app/old.go", Line: 42}},
		Files:              []domain.File{{Path: "internal/app/old.go"}},
		RequiredValidation: []string{"build", "test"},
	}
	plan := ts.assemblePlan("Rename OldName to NewName", pkt)
	joined := strings.Join(plan.ImplementationSteps, "\n")
	for _, want := range []string{
		"Rename OldName to NewName (definition in the affected components above)",
		"Update all references to OldName",
		"Update OldName (internal/app/old.go:42)",
		"Ensure the project builds (go build ./...).",
		"Add/update tests for affected symbols and run go test.",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("implementation steps missing %q:\n%s", want, joined)
		}
	}
}

// TestAssemblePlanFiltersUnrelatedDependencies verifies that dependency edges
// unrelated to the affected components (symbol names / file paths) are
// filtered out of the plan, while edges touching them — including via a
// "pkg."-qualified right side — are kept.
func TestAssemblePlanFiltersUnrelatedDependencies(t *testing.T) {
	ts := &TaskService{}
	pkt := domain.ContextPacket{
		Symbols: []domain.Symbol{{Name: "Target", File: "internal/app/target.go", Line: 7}},
		Files:   []domain.File{{Path: "internal/app/target.go"}},
		Dependencies: []domain.Edge{
			{From: "internal/app", To: "internal/context"},         // unrelated package edge
			{From: "internal/app/target.go", To: "internal/index"}, // originates in the affected file
			{From: "Target", To: "Helper"},                         // touches the affected symbol
			{From: "pkg.Other", To: "Target"},                      // touches the affected symbol after trimming
		},
	}
	plan := ts.assemblePlan("Fix Target", pkt)
	joined := strings.Join(plan.Dependencies, "\n")
	if strings.Contains(joined, "internal/app → internal/context") {
		t.Errorf("unrelated package edge must be filtered out:\n%s", joined)
	}
	for _, want := range []string{
		"internal/app/target.go → internal/index",
		"Target → Helper",
		"pkg.Other → Target",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("edge touching affected components missing %q:\n%s", want, joined)
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

// TestRunWorkflowRecordsToolDecisionTraces verifies the wiring:
// when a TaskService is given a trace recorder, every workflow step that runs
// through RunWorkflow records a ToolDecisionTrace (tool, why, expected output,
// actual output, latency) so the tool-selection trail is auditable rather than
// an in-memory return value.
func TestRunWorkflowRecordsToolDecisionTraces(t *testing.T) {
	svc, _ := newTestTaskService(t)
	rec := NewToolDecisionTraceRecorder()
	svc.traceRec = rec

	// Register the agents the workflow steps route to.
	reg := svc.Registry()
	for _, a := range []agent.Agent{
		{Agent: domain.Agent{ID: "planner", Type: "planner", Name: "Planner"}},
		{Agent: domain.Agent{ID: "coder", Type: "coder", Name: "Coder"}},
		{Agent: domain.Agent{ID: "reviewer", Type: "reviewer", Name: "Reviewer"}},
	} {
		if err := reg.Register(a); err != nil {
			t.Fatalf("register %s: %v", a.Type, err)
		}
	}

	_, _ = svc.RunWorkflow("add caching to UserService", func(action string, t *agent.Task) (string, error) {
		return "ok", nil
	})

	traces := rec.Traces()
	if len(traces) == 0 {
		t.Fatal("no tool-decision traces recorded by RunWorkflow")
	}
	// Steps that ran before the approval gate (request/analyze/plan) must have
	// recorded traces with a tool and expected/actual output.
	for _, tr := range traces {
		if tr.Tool == "" {
			t.Errorf("trace with empty tool: %+v", tr)
		}
		if tr.WhySelected == "" {
			t.Errorf("trace %s has empty why_selected", tr.Tool)
		}
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
