package domain

// IntentType classifies the kind of engineering request. Strict Plan Phase 6:
// 10 intent types.
type IntentType string

const (
	IntentUnderstand    IntentType = "UNDERSTAND"
	IntentCodeChange    IntentType = "CODE_CHANGE"
	IntentReview        IntentType = "REVIEW"
	IntentWhatIf        IntentType = "WHAT_IF"
	IntentIncident      IntentType = "INCIDENT"
	IntentModernization IntentType = "MODERNIZATION"
	IntentSecurity      IntentType = "SECURITY"
	IntentTest          IntentType = "TEST"
	IntentDeploy        IntentType = "DEPLOY"
	IntentAudit         IntentType = "AUDIT"
)

// CompiledIntent is the output of the Intent Compiler. Strict Plan Phase 6 P0.
type CompiledIntent struct {
	Type            IntentType `json:"intent_type"`
	Objective       string     `json:"objective"`
	Target          string     `json:"target"`
	Scope           string     `json:"scope"`
	Environment     string     `json:"environment"`
	DesiredOutcome  string     `json:"desired_outcome"`
	RawText         string     `json:"raw_text"`
}

// WorkflowID identifies one of the 5 primary user workflows (A-E).
type WorkflowID string

const (
	WorkflowUnderstand WorkflowID = "A_UNDERSTAND"
	WorkflowSafeChange WorkflowID = "B_SAFE_CHANGE"
	WorkflowPredict    WorkflowID = "C_PREDICT"
	WorkflowOperate    WorkflowID = "D_OPERATE"
	WorkflowGovern     WorkflowID = "E_GOVERN"
)

// RunResult is the output of kern_run. Strict Plan Phase 6 P0: returns Task,
// workflow, required capabilities, selected tools, selected agents, context
// plan, risk, approval state, next action.
type RunResult struct {
	TaskID        string            `json:"task_id"`
	Workflow      WorkflowID        `json:"workflow"`
	Intent        CompiledIntent    `json:"intent"`
	Capabilities  []string          `json:"capabilities"`
	Tools         []string          `json:"tools"`
	Agents        []string          `json:"agents"`
	ContextPlan   string            `json:"context_plan"`
	Risk          Risk              `json:"risk"`
	ApprovalState string            `json:"approval_state"` // "none", "required", "granted"
	NextAction    string            `json:"next_action"`
}

// Capability describes a kern capability that can be selected for a task.
// Strict Plan Phase 6 P1.
type Capability struct {
	Name         string   `json:"name"`
	Inputs       []string `json:"inputs"`
	Dependencies []string `json:"dependencies"`
	Tools        []string `json:"tools"`
	Permissions  []string `json:"permissions"`
	Risk         string   `json:"risk"` // "low", "medium", "high"
	Outputs      []string `json:"outputs"`
	Artifacts    []string `json:"artifacts"`
}

// ToolDecisionTrace records why a tool was selected and what it produced.
// Strict Plan Phase 6 P1.
type ToolDecisionTrace struct {
	Tool          string  `json:"tool"`
	WhySelected   string  `json:"why_selected"`
	Inputs        string  `json:"inputs"`
	ExpectedOutput string `json:"expected_output"`
	ActualOutput  string  `json:"actual_output"`
	Cost          float64 `json:"cost"`
	Latency       float64 `json:"latency_ms"`
}
