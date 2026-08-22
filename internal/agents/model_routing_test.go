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
