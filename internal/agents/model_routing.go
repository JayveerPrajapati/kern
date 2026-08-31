package agents

import (
	"os"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// RouteModel selects a model/provider for a task based on complexity, risk, and
// cost. : model routing.
// Routing logic (deterministic, no LLM):
// - HIGH risk + HIGH complexity → most capable model (e.g. large model)
// - MEDIUM risk + MEDIUM complexity → balanced model
// - LOW risk + LOW complexity → fast/cheap model
// - Default → balanced model
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

// ModelOverride returns the role-specific model override from the environment
// (KERN_MODEL_<ROLE>, falling back to KERN_MODEL_DEFAULT), or "" when no
// override is set. Production agent entry points (coder/planner) use this to
// let operators steer the model per role without forcing a provider-specific
// hardcoded default. It is the production-facing entry point for model routing:
// unlike RouteModel/RouteModelForTask, it does NOT fall back to a hardcoded
// heuristic default, so an unset override leaves the provider's own default
// in effect (provider-neutral).
func ModelOverride(role Role) string {
	if env := os.Getenv(modelEnvVar(role)); env != "" {
		return env
	}
	return os.Getenv("KERN_MODEL_DEFAULT")
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

// EvaluateAgent creates an AgentEvaluation from execution metrics. The
// duration is a time.Duration; unlike the earlier placeholder
// (P9.7), it is now actually recorded on the evaluation so duration feeds the
// scoring/score model.
func EvaluateAgent(agentID, taskID string, success bool, tokens int, cost float64, duration time.Duration, retries int, humanIntervention bool, defects int) domain.AgentEvaluation {
	return domain.AgentEvaluation{
		AgentID:           agentID,
		TaskID:            taskID,
		Success:           success,
		TokensUsed:        tokens,
		Cost:              cost,
		Duration:          duration,
		Retries:           retries,
		HumanIntervention: humanIntervention,
		Defects:           defects,
	}
}

// routeModelWithFactors enriches the base routing with P9.4/P9.5 factors:
// historical success and language. A language that the "most capable" model is
// weak on, or a low historical success for the current choice, can downgrade or
// upgrade the model. The decision is deterministic given the factors.
func routeModelWithFactors(base domain.ModelRoutingDecision, f domain.RoutingFactors) domain.ModelRoutingDecision {
	reason := base.Reason
	// Language factor: for the high-complexity pick, some languages prefer a
	// specialized model. We keep the pick but note it.
	if f.Language != "" {
		reason += "; language=" + f.Language
	}
	// Historical success: if the candidate's past success is high and it is
	// already the most capable model, keep it. If historical success is very low
	// (<= 0.4) and the base is NOT the most capable, promote to most capable.
	if f.HistoricalSuccess > 0 {
		if f.HistoricalSuccess <= 0.4 && base.Model != "llama3.1:70b" {
			base = routeModelBase(domain.RiskHigh, "high")
			reason = "historical success low; promoted to most capable model"
		} else if f.HistoricalSuccess >= 0.9 && base.Model == "llama3.1:70b" {
			base = routeModelBase(domain.RiskLow, "low")
			reason = "historical success high; demoted to cheap model"
		}
	}
	base.Reason = reason
	return base
}

// RouteModelForTask selects a model for a task given intent kind, risk,
// complexity, and richer routing factors (P9.4/P9.5). It is the entry point the
// orchestrator uses; it combines the kind-level defaults with the factor
// adjustments and honors the same env overrides as RouteModel.
func RouteModelForTask(role Role, risk domain.RiskLevel, complexity string, f domain.RoutingFactors) domain.ModelRoutingDecision {
	decision := routeModelWithFactors(routeModelBase(risk, complexity), f)
	// Honor env overrides (same precedence as RouteModel).
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

// modelScore is the same weighted score used by agentScore but reusable for
// model A/B so both comparisons share one scoring model.
func modelScore(ev domain.AgentEvaluation) float64 {
	return agentScore(ev)
}

// EvaluateModel records an AgentEvaluation for a single model candidate,
// tagging the evaluation with the model so model A/B comparisons can attribute
// the result (P9.8).
func EvaluateModel(model, taskID string, success bool, tokens int, cost float64, duration time.Duration) domain.AgentEvaluation {
	ev := EvaluateAgent(model, taskID, success, tokens, cost, duration, 0, false, 0)
	ev.Model = model
	return ev
}

// CompareModels performs a model A/B comparison (P9.8): it routes two candidate
// models for the same task and scores their evaluations, returning the winning
// model and the metric deltas. A model may be selected explicitly via
// candidateA/candidateB (non-empty), otherwise the router picks from risk.
func CompareModels(taskID string, candidateA, candidateB domain.ModelRoutingDecision, evalA, evalB domain.AgentEvaluation) domain.ModelComparison {
	if candidateA.Model == "" {
		candidateA = routeModelBase(domain.RiskMedium, "medium")
	}
	if candidateB.Model == "" {
		candidateB = routeModelBase(domain.RiskHigh, "high")
	}
	scoreA := modelScore(evalA)
	scoreB := modelScore(evalB)
	winner := "tie"
	if scoreA > scoreB {
		winner = candidateA.Model
	} else if scoreB > scoreA {
		winner = candidateB.Model
	}
	return domain.ModelComparison{
		TaskID: taskID,
		ModelA: candidateA, ModelB: candidateB,
		EvalA: evalA, EvalB: evalB,
		Winner: winner,
		Metrics: map[string][2]float64{
			"score":   {scoreA, scoreB},
			"cost":    {evalA.Cost, evalB.Cost},
			"tokens":  {float64(evalA.TokensUsed), float64(evalB.TokensUsed)},
			"defects": {float64(evalA.Defects), float64(evalB.Defects)},
		},
	}
}

// CompareAgents performs an A/B comparison between two agent evaluations.
// The winner is determined by a weighted score:
// success (×100) - cost (×10) - tokens (×0.001) - duration_seconds (×0.1) - defects (×20) - retries (×5)
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
