// Org audit wire shapes. The org approvals store records every org approval
// event (create/approve/reject/consume) on the org audit trail; these DTOs
// are the shared wire layer for rendering org audit entries on the org
// surface (serveOrgAudit in internal/enterprise). They moved here from
// internal/enterprise (P13 stage 3) so the org governance-records surface —
// approvals + the audit entries that record them — lives in one package.
package orgapprovals

import (
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// RiskJSON is the flattened wire shape for the risk attached to an audit
// entry (the level/score/decision flags that matter for audit consumers).
type RiskJSON struct {
	Level            string   `json:"level"`
	Score            float64  `json:"score"`
	Factors          []string `json:"factors,omitempty"`
	Mitigation       string   `json:"mitigation,omitempty"`
	Blocked          bool     `json:"blocked"`
	ApprovalRequired bool     `json:"approval_required"`
}

// AuditEntryJSON is the wire shape for audit entries (snake_case).
type AuditEntryJSON struct {
	ID                string    `json:"id"`
	Timestamp         time.Time `json:"timestamp"`
	AgentID           string    `json:"agent_id"`
	Action            string    `json:"action"`
	Resource          string    `json:"resource"`
	Risk              RiskJSON  `json:"risk"`
	Approved          bool      `json:"approved"`
	Result            string    `json:"result"`
	Hash              string    `json:"hash"`
	TaskID            string    `json:"task_id,omitempty"`
	Policy            string    `json:"policy,omitempty"`
	Reason            string    `json:"reason,omitempty"`
	ValidationOutcome any       `json:"validation_outcome,omitempty"`
}

// AuditEntryJSONOf projects a governance.AuditEntry onto the wire shape.
func AuditEntryJSONOf(e governance.AuditEntry) AuditEntryJSON {
	out := AuditEntryJSON{
		ID:        e.ID,
		Timestamp: e.Timestamp,
		AgentID:   e.AgentID,
		Action:    e.Action,
		Resource:  e.Resource,
		Approved:  e.Approved,
		Result:    e.Result,
		Hash:      e.Hash,
		TaskID:    e.TaskID,
		Policy:    e.Policy,
		Reason:    e.Reason,
		Risk: RiskJSON{
			Level:            string(e.Risk.Level),
			Score:            e.Risk.Score,
			Factors:          e.Risk.Factors,
			Mitigation:       e.Risk.Mitigation,
			Blocked:          e.Risk.Blocked,
			ApprovalRequired: e.Risk.ApprovalRequired,
		},
	}
	if e.ValidationOutcome != nil {
		out.ValidationOutcome = e.ValidationOutcome
	}
	return out
}
