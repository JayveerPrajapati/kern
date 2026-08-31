package agents

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestRouteModelHighRisk(t *testing.T) {
	d := RouteModel(RoleCoder, domain.RiskCritical, "high")
	if d.Complexity != "high" {
		t.Errorf("Complexity=%s, want high", d.Complexity)
	}
	if d.Model == "" {
		t.Error("Model should not be empty")
	}
}

func TestRouteModelLowRisk(t *testing.T) {
	d := RouteModel(RoleCoder, domain.RiskLow, "low")
	if d.Complexity != "low" {
		t.Errorf("Complexity=%s, want low", d.Complexity)
	}
}

func TestRouteModelEnvOverride(t *testing.T) {
	// Role-specific env var wins over the hardcoded default.
	t.Setenv("KERN_MODEL_CODER", "gpt-4o")
	d := RouteModel(RoleCoder, domain.RiskLow, "low")
	if d.Model != "gpt-4o" {
		t.Errorf("Model=%s, want gpt-4o (KERN_MODEL_CODER override)", d.Model)
	}

	// After unsetting, the hardcoded default is returned.
	t.Setenv("KERN_MODEL_CODER", "")
	d = RouteModel(RoleCoder, domain.RiskLow, "low")
	if d.Model != "llama3.2:3b" {
		t.Errorf("Model=%s, want llama3.2:3b (hardcoded default)", d.Model)
	}
}

func TestRouteModelDefaultEnvFallback(t *testing.T) {
	// KERN_MODEL_DEFAULT applies to roles without a specific override.
	t.Setenv("KERN_MODEL_DEFAULT", "mistral")
	t.Setenv("KERN_MODEL_SRE", "")
	d := RouteModel(RoleSRE, domain.RiskLow, "low")
	if d.Model != "mistral" {
		t.Errorf("Model=%s, want mistral (KERN_MODEL_DEFAULT fallback)", d.Model)
	}
}

func TestModelOverride(t *testing.T) {
	// No override set -> empty (provider default stays in effect).
	t.Setenv("KERN_MODEL_DEFAULT", "")
	t.Setenv("KERN_MODEL_CODER", "")
	if got := ModelOverride(RoleCoder); got != "" {
		t.Errorf("ModelOverride(RoleCoder) with no env = %q, want empty", got)
	}

	// Role-specific override wins.
	t.Setenv("KERN_MODEL_CODER", "codellama")
	if got := ModelOverride(RoleCoder); got != "codellama" {
		t.Errorf("ModelOverride(RoleCoder) = %q, want codellama", got)
	}

	// Role without its own var falls back to KERN_MODEL_DEFAULT.
	t.Setenv("KERN_MODEL_ARCHITECT", "")
	t.Setenv("KERN_MODEL_DEFAULT", "default-model")
	if got := ModelOverride(RoleArchitect); got != "default-model" {
		t.Errorf("ModelOverride(RoleArchitect) = %q, want default-model (KERN_MODEL_DEFAULT fallback)", got)
	}

	// Explicit empty role-specific var must NOT shadow the default.
	t.Setenv("KERN_MODEL_CODER", "")
	if got := ModelOverride(RoleCoder); got != "default-model" {
		t.Errorf("ModelOverride(RoleCoder) with empty KERN_MODEL_CODER = %q, want default-model", got)
	}
}

func TestCompareAgentsWinner(t *testing.T) {
	a := domain.AgentEvaluation{AgentID: "a1", Success: true, TokensUsed: 1000, Cost: 0.01, Duration: 5 * time.Second, Defects: 0}
	b := domain.AgentEvaluation{AgentID: "a2", Success: false, TokensUsed: 5000, Cost: 0.10, Duration: 30 * time.Second, Defects: 3}
	cmp := CompareAgents("t1", a, b)
	if cmp.Winner != "a1" {
		t.Errorf("Winner=%s, want a1 (success + lower cost/tokens/defects)", cmp.Winner)
	}
}

func TestCompareAgentsTie(t *testing.T) {
	a := domain.AgentEvaluation{AgentID: "a1", Success: true, TokensUsed: 1000, Cost: 0.01, Duration: 5 * time.Second}
	b := domain.AgentEvaluation{AgentID: "a2", Success: true, TokensUsed: 1000, Cost: 0.01, Duration: 5 * time.Second}
	cmp := CompareAgents("t1", a, b)
	if cmp.Winner != "tie" {
		t.Errorf("Winner=%s, want tie (identical metrics)", cmp.Winner)
	}
}

func TestRouteModelWithFactors(t *testing.T) {
	// Low historical success on the balanced model -> promote to most capable.
	base := routeModelBase(domain.RiskMedium, "medium")
	got := routeModelWithFactors(base, domain.RoutingFactors{HistoricalSuccess: 0.2})
	if got.Model != "llama3.1:70b" {
		t.Errorf("promoted model = %q, want llama3.1:70b", got.Model)
	}
	// High historical success on the most capable -> demote to cheap.
	baseHigh := routeModelBase(domain.RiskHigh, "high")
	got2 := routeModelWithFactors(baseHigh, domain.RoutingFactors{HistoricalSuccess: 0.95})
	if got2.Model == "llama3.1:70b" {
		t.Errorf("should demote off most capable, got %q", got2.Model)
	}
	// Language is noted in the reason but does not change the model.
	got3 := routeModelWithFactors(base, domain.RoutingFactors{Language: "go"})
	if got3.Model != base.Model {
		t.Errorf("language should not change model, got %q", got3.Model)
	}
}

func TestRouteModelForTaskFactors(t *testing.T) {
	// Neutral factors route to medium.
	d := RouteModelForTask(RoleCoder, domain.RiskMedium, "medium", domain.RoutingFactors{})
	if d.Model == "" {
		t.Error("expected a model")
	}
	// Low historical success biases toward most capable.
	d2 := RouteModelForTask(RoleCoder, domain.RiskMedium, "medium", domain.RoutingFactors{HistoricalSuccess: 0.1})
	if d2.Model != "llama3.1:70b" {
		t.Errorf("low success should route to 70b, got %q", d2.Model)
	}
}

func TestEvaluateAgentRecordsDuration(t *testing.T) {
	ev := EvaluateAgent("a1", "t1", true, 100, 0.1, 5*time.Second, 0, false, 0)
	if ev.Duration != 5*time.Second {
		t.Errorf("Duration = %v, want 5s (P9.7)", ev.Duration)
	}
}

func TestCompareModels(t *testing.T) {
	evA := domain.AgentEvaluation{AgentID: "a1", Success: true, TokensUsed: 100, Cost: 0.1}
	evB := domain.AgentEvaluation{AgentID: "b1", Success: false, TokensUsed: 500, Cost: 0.5}
	mc := CompareModels("t1", domain.ModelRoutingDecision{}, domain.ModelRoutingDecision{}, evA, evB)
	// evA (success, cheaper) outscores evB, so candidateA (default 8b) wins.
	if mc.Winner != mc.ModelA.Model {
		t.Errorf("winner = %q, want the model behind the better eval (A=%q)", mc.Winner, mc.ModelA.Model)
	}
	if mc.Metrics["score"][0] <= mc.Metrics["score"][1] {
		t.Errorf("A should outscore B: %+v", mc.Metrics)
	}
}

func TestEvaluateModel(t *testing.T) {
	ev := EvaluateModel("m1", "t1", true, 50, 0.05, 1*time.Second)
	if ev.AgentID != "m1" || !ev.Success || ev.Model != "m1" {
		t.Errorf("EvaluateModel = %+v", ev)
	}
}
