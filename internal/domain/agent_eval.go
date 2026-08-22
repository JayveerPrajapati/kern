package domain

import "time"

// ModelRoutingDecision selects a model/provider for a task based on complexity,
// risk, and cost. Strict Plan Phase 9 P1.
type ModelRoutingDecision struct {
	Provider     string  `json:"provider"`      // "ollama", "openai", "anthropic"
	Model        string  `json:"model"`         // model name
	Reason       string  `json:"reason"`        // why this model was selected
	Complexity   string  `json:"complexity"`    // "low", "medium", "high"
	EstimatedCost float64 `json:"estimated_cost"`
}

// AgentEvaluation tracks the performance of an agent on a task. Strict Plan
// Phase 9 P2.
type AgentEvaluation struct {
	AgentID            string        `json:"agent_id"`
	TaskID             string        `json:"task_id"`
	Success            bool          `json:"success"`
	TokensUsed         int           `json:"tokens_used"`
	Cost               float64       `json:"cost"`
	Duration           time.Duration `json:"duration"`
	Retries            int           `json:"retries"`
	HumanIntervention  bool          `json:"human_intervention"`
	Defects            int           `json:"defects"` // bugs found in review
	ProductionOutcome  string        `json:"production_outcome,omitempty"` // "healthy", "incident", "unknown"
}

// AgentComparison is the result of an A/B comparison between two agents on the
// same task. Strict Plan Phase 9 P2.
type AgentComparison struct {
	TaskID   string            `json:"task_id"`
	AgentA   AgentEvaluation   `json:"agent_a"`
	AgentB   AgentEvaluation   `json:"agent_b"`
	Winner   string            `json:"winner"`   // agent ID of the winner, or "tie"
	Metrics  map[string][2]float64 `json:"metrics"` // metric name → [valueA, valueB]
}
