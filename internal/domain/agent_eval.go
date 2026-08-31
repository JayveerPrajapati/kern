package domain

import "time"

// ModelRoutingDecision selects a model/provider for a task based on complexity,
// risk, and cost. .
type ModelRoutingDecision struct {
	Provider      string  `json:"provider"`   // "ollama", "openai", "anthropic"
	Model         string  `json:"model"`      // model name
	Reason        string  `json:"reason"`     // why this model was selected
	Complexity    string  `json:"complexity"` // "low", "medium", "high"
	EstimatedCost float64 `json:"estimated_cost"`
}

// RoutingFactors captures the additional signals a router may consider beyond
// intent kind (P9.4 richer routing, P9.5 model routing factors). All are
// optional: a zero-valued factor does not bias routing.
type RoutingFactors struct {
	// Risk is the assessed risk of the task (low/medium/high/critical).
	Risk string `json:"risk,omitempty"`
	// Language is the primary source language ("go", "python", "typescript", ...).
	Language string `json:"language,omitempty"`
	// HistoricalSuccess is the prior success rate (0.0-1.0) of the candidate
	// agent/model on similar tasks. Zero (unknown) is treated as neutral.
	HistoricalSuccess float64 `json:"historical_success,omitempty"`
	// HistoricalCost is the average cost of prior runs of the candidate.
	HistoricalCost float64 `json:"historical_cost,omitempty"`
}

// AgentEvaluation tracks the performance of an agent on a task.
type AgentEvaluation struct {
	AgentID           string        `json:"agent_id"`
	TaskID            string        `json:"task_id"`
	Success           bool          `json:"success"`
	TokensUsed        int           `json:"tokens_used"`
	Cost              float64       `json:"cost"`
	Duration          time.Duration `json:"duration"`
	Retries           int           `json:"retries"`
	HumanIntervention bool          `json:"human_intervention"`
	Defects           int           `json:"defects"`                      // bugs found in review
	ProductionOutcome string        `json:"production_outcome,omitempty"` // "healthy", "incident", "unknown"
	// Model records which model the agent used (P9.8 model A/B).
	Model string `json:"model,omitempty"`
}

// AgentComparison is the result of an A/B comparison between two agents on the
// same task. .
type AgentComparison struct {
	TaskID  string                `json:"task_id"`
	AgentA  AgentEvaluation       `json:"agent_a"`
	AgentB  AgentEvaluation       `json:"agent_b"`
	Winner  string                `json:"winner"`  // agent ID of the winner, or "tie"
	Metrics map[string][2]float64 `json:"metrics"` // metric name → [valueA, valueB]
}

// ModelComparison is the result of a model A/B comparison (P9.8): two candidate
// models routed for the same task, each with a routing decision and an
// evaluation, plus the winning model and the scoring metrics.
type ModelComparison struct {
	TaskID  string                `json:"task_id"`
	ModelA  ModelRoutingDecision  `json:"model_a"`
	ModelB  ModelRoutingDecision  `json:"model_b"`
	EvalA   AgentEvaluation       `json:"eval_a"`
	EvalB   AgentEvaluation       `json:"eval_b"`
	Winner  string                `json:"winner"`  // model A or model B, or "tie"
	Metrics map[string][2]float64 `json:"metrics"` // metric → [valueA, valueB]
}
