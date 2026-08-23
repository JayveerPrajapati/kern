package agents

import (
	"encoding/json"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// AgentResult is the result contract for a specialist agent step (Phase 9.3).
// It bundles everything an agent produces for a task into one serializable
// value: the outcome text, the supporting evidence, the risks it identified,
// a confidence score, any artifacts it wrote, and a recommended next action.
//
// The value is deliberately an additive result contract: every field is a
// value type or a slice, so a zero-value AgentResult is valid and serializes
// cleanly. Evidence reuses internal/domain.Claim (built via internal/evidence),
// and Risks reuse internal/domain.Risk — no bespoke claim/risk types here.
type AgentResult struct {
	// TaskID is the task this result was produced for.
	TaskID string `json:"task_id"`
	// Agent is the specialist identity that produced the result.
	Agent string `json:"agent"`
	// Status is the outcome: "success", "failure", or "blocked".
	Status string `json:"status"`
	// Result is the free-text outcome of the agent's step.
	Result string `json:"result"`
	// Evidence is the typed claims supporting the result.
	Evidence []domain.Claim `json:"evidence"`
	// Risks is the risk assessment for the proposed action.
	Risks []domain.Risk `json:"risks"`
	// Confidence is the 0.0-1.0 confidence in the result.
	Confidence float64 `json:"confidence"`
	// Artifacts lists artifact IDs/paths the agent produced.
	Artifacts []string `json:"artifacts"`
	// RecommendedAction is the next action the agent recommends.
	RecommendedAction string `json:"recommended_action"`
}

// Result status values.
const (
	// ResultSuccess marks a step that completed successfully.
	ResultSuccess = "success"
	// ResultFailure marks a step that failed.
	ResultFailure = "failure"
	// ResultBlocked marks a step that could not proceed (e.g. approval gate).
	ResultBlocked = "blocked"
)

// NewAgentResult builds a well-formed, empty-but-non-nil AgentResult for a task
// and agent. Slice fields are initialized to empty slices (not nil) so a result
// serializes as `[]` rather than `null`, and the confidence defaults to 1.0
// until the producing agent overrides it with a lower score.
func NewAgentResult(taskID, agent string) *AgentResult {
	return &AgentResult{
		TaskID:     taskID,
		Agent:      agent,
		Status:     ResultSuccess,
		Evidence:   []domain.Claim{},
		Risks:      []domain.Risk{},
		Artifacts:  []string{},
		Confidence: 1.0,
	}
}

// ProduceResult builds a result contract for this specialist on a task. It is
// the specialist execution path: a stage handler can create its result contract
// directly off the specialist that ran, guaranteeing the Agent field matches
// the specialist identity.
func (s *Specialist) ProduceResult(taskID string) *AgentResult {
	agent := s.Agent.ID
	if agent == "" {
		agent = string(s.Role)
	}
	return NewAgentResult(taskID, agent)
}

// WithStatus sets the outcome status.
func (r *AgentResult) WithStatus(status string) *AgentResult {
	r.Status = status
	return r
}

// WithResult sets the outcome text.
func (r *AgentResult) WithResult(text string) *AgentResult {
	r.Result = text
	return r
}

// WithConfidence sets the confidence score, clamped to the valid [0,1] range so
// an out-of-range value can never corrupt the contract.
func (r *AgentResult) WithConfidence(c float64) *AgentResult {
	switch {
	case c < 0:
		r.Confidence = 0
	case c > 1:
		r.Confidence = 1
	default:
		r.Confidence = c
	}
	return r
}

// AddEvidence appends a supporting claim.
func (r *AgentResult) AddEvidence(c domain.Claim) *AgentResult {
	r.Evidence = append(r.Evidence, c)
	return r
}

// AddRisk appends a risk assessment.
func (r *AgentResult) AddRisk(risk domain.Risk) *AgentResult {
	r.Risks = append(r.Risks, risk)
	return r
}

// AddArtifact appends an artifact ID/path.
func (r *AgentResult) AddArtifact(id string) *AgentResult {
	r.Artifacts = append(r.Artifacts, id)
	return r
}

// WithRecommendedAction sets the recommended next action.
func (r *AgentResult) WithRecommendedAction(action string) *AgentResult {
	r.RecommendedAction = action
	return r
}

// MarshalJSON implements json.Marshaler so the slices always serialize as
// arrays rather than null.
func (r AgentResult) MarshalJSON() ([]byte, error) {
	type alias AgentResult
	if r.Evidence == nil {
		r.Evidence = []domain.Claim{}
	}
	if r.Risks == nil {
		r.Risks = []domain.Risk{}
	}
	if r.Artifacts == nil {
		r.Artifacts = []string{}
	}
	return json.Marshal(alias(r))
}
