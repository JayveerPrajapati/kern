package agents

import (
	"os"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// RouteModel selects a model/provider for a task based on complexity, risk, and
// cost. Strict Plan Phase 9 P1: model routing.
//
// Routing logic (deterministic, no LLM):
//   - HIGH risk + HIGH complexity → most capable model (e.g. large model)
//   - MEDIUM risk + MEDIUM complexity → balanced model
//   - LOW risk + LOW complexity → fast/cheap model
//   - Default → balanced model
//
// Per-specialist model selection is honored via environment variables:
// KERN_MODEL_PLANNER, KERN_MODEL_ARCHITECT, KERN_MODEL_CODER,
// KERN_MODEL_REVIEWER, KERN_MODEL_SECURITY, KERN_MODEL_TESTER, KERN_MODEL_SRE.
// A role-specific var takes precedence over KERN_MODEL_DEFAULT, which in turn
// takes precedence over the hardcoded defaults computed from risk/complexity.
func RouteModel(role Role, risk domain.RiskLevel, complexity string) domain.ModelRoutingDecision {
	decision := routeModelBase(risk, complexity)
	if env := os.Getenv(modelEnvVar(role)); env != "" {
		decision.Model = env
		decision.Reason = "role-specific KERN_MODEL_* override"
		return decision
	}
	if env := os.Getenv("KERN_MODEL_DEFAULT"); env != "" {
		decision.Model = env
		decision.Reason = "KERN_MODEL_DEFAULT override"
		return decision
	}
	return decision
}

// modelEnvVar returns the KERN_MODEL_* environment variable that overrides the
// model for a given specialist role. Roles without a dedicated variable map to
// KERN_MODEL_DEFAULT.
func modelEnvVar(role Role) string {
	switch role {
	case RolePlanner:
		return "KERN_MODEL_PLANNER"
	case RoleArchitect:
		return "KERN_MODEL_ARCHITECT"
	case RoleCoder:
		return "KERN_MODEL_CODER"
	case RoleReviewer:
		return "KERN_MODEL_REVIEWER"
	case RoleSecurity:
		return "KERN_MODEL_SECURITY"
	case RoleTester:
		return "KERN_MODEL_TESTER"
	case RoleSRE:
		return "KERN_MODEL_SRE"
	default:
		return "KERN_MODEL_DEFAULT"
	}
}

// routeModelBase computes the deterministic default model selection from risk
// and complexity, ignoring any environment overrides.
func routeModelBase(risk domain.RiskLevel, complexity string) domain.ModelRoutingDecision {
	switch {
	case risk == domain.RiskCritical || risk == domain.RiskHigh || complexity == "high":
		return domain.ModelRoutingDecision{
			Provider: "ollama", Model: "llama3.1:70b",
			Reason:     "high risk/complexity requires most capable model",
			Complexity: "high", EstimatedCost: 0.50,
		}
	case risk == domain.RiskMedium || complexity == "medium":
		return domain.ModelRoutingDecision{
			Provider: "ollama", Model: "llama3.1:8b",
			Reason:     "medium risk/complexity: balanced model",
			Complexity: "medium", EstimatedCost: 0.10,
		}
	default:
		return domain.ModelRoutingDecision{
			Provider: "ollama", Model: "llama3.2:3b",
			Reason:     "low risk/complexity: fast/cheap model",
			Complexity: "low", EstimatedCost: 0.02,
		}
	}
}

// EvaluateAgent creates an AgentEvaluation from execution metrics. Strict Plan
// Phase 9 P2: agent evaluation.
func EvaluateAgent(agentID, taskID string, success bool, tokens int, cost float64, duration interface{}, retries int, humanIntervention bool, defects int) domain.AgentEvaluation {
	ev := domain.AgentEvaluation{
		AgentID: agentID, TaskID: taskID, Success: success,
		TokensUsed: tokens, Cost: cost, Retries: retries,
		HumanIntervention: humanIntervention, Defects: defects,
	}
	if d, ok := duration.(domain.AgentEvaluation); ok {
		_ = d // placeholder for type assertion
	}
	return ev
}

// CompareAgents performs an A/B comparison between two agent evaluations.
// Strict Plan Phase 9 P2.
//
// The winner is determined by a weighted score:
//   success (×100) - cost (×10) - tokens (×0.001) - duration_seconds (×0.1) - defects (×20) - retries (×5)
func CompareAgents(taskID string, a, b domain.AgentEvaluation) domain.AgentComparison {
	scoreA := agentScore(a)
	scoreB := agentScore(b)
	winner := "tie"
	if scoreA > scoreB {
		winner = a.AgentID
	} else if scoreB > scoreA {
		winner = b.AgentID
	}
	return domain.AgentComparison{
		TaskID: taskID,
		AgentA: a,
		AgentB: b,
		Winner: winner,
		Metrics: map[string][2]float64{
			"score":   {scoreA, scoreB},
			"tokens":  {float64(a.TokensUsed), float64(b.TokensUsed)},
			"cost":    {a.Cost, b.Cost},
			"defects": {float64(a.Defects), float64(b.Defects)},
		},
	}
}

func agentScore(ev domain.AgentEvaluation) float64 {
	score := 0.0
	if ev.Success {
		score += 100
	}
	score -= ev.Cost * 10
	score -= float64(ev.TokensUsed) * 0.001
	score -= ev.Duration.Seconds() * 0.1
	score -= float64(ev.Defects) * 20
	score -= float64(ev.Retries) * 5
	if ev.HumanIntervention {
		score -= 30
	}
	return score
}
