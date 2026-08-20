// Package firewall provides the unified AI change firewall that ties together
// agent identity, risk scoring, the approval workflow, and the audit log.
package firewall

import (
	"fmt"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	appr "github.com/JayveerPrajapati/kern/internal/governance/approval"
	"github.com/JayveerPrajapati/kern/internal/governance/audit"
	"github.com/JayveerPrajapati/kern/internal/governance/identity"
	"github.com/JayveerPrajapati/kern/internal/governance/risk"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// Firewall is the unified AI change firewall. Every agent action passes through
// it in the order: Authentication → Authorization → Permission → Risk → Impact
// → Policy → Approval → Execution. It fails closed: unknown agents, missing
// permissions, and always-blocked actions are denied by default.
type Firewall struct {
	agents       map[string]*identity.AgentIdentity
	assessor     *risk.RiskAssessor
	approval     *appr.ApprovalWorkflow
	audit        *audit.AuditLog
	approvedKeys map[string]bool
	bus          *eventbus.Bus // optional event publisher; nil = no-op
}

// NewFirewall creates a new change firewall with the default policies. No
// agents are registered; call WithAgents to add them.
func NewFirewall() *Firewall {
	return &Firewall{
		agents:       map[string]*identity.AgentIdentity{},
		assessor:     risk.NewRiskAssessor(risk.DefaultPolicies()),
		approval:     appr.NewApprovalWorkflow(),
		audit:        audit.NewAuditLog(),
		approvedKeys: map[string]bool{},
	}
}

// WithAgents registers agents that can act through the firewall. It returns
// the firewall for chaining.
func (f *Firewall) WithAgents(agents ...*identity.AgentIdentity) *Firewall {
	for _, a := range agents {
		if a != nil {
			f.agents[a.ID] = a
		}
	}
	return f
}

// WithPolicies sets custom risk policies (overrides the defaults). It returns
// the firewall for chaining.
func (f *Firewall) WithPolicies(policies []domain.Policy) *Firewall {
	f.assessor = risk.NewRiskAssessor(policies)
	return f
}

// WithBus attaches an optional event bus. When non-nil, the firewall publishes
// policy.evaluated, policy.blocked and approval.requested events at the
// relevant transition points. A nil bus is a no-op (firewall still works).
func (f *Firewall) WithBus(b *eventbus.Bus) *Firewall {
	f.bus = b
	return f
}

// Policies returns a copy of the risk policies currently loaded. It is the
// additive accessor callers (e.g. the context engine) use to surface the
// governance rules that apply to a change scope.
func (f *Firewall) Policies() []domain.Policy {
	return f.assessor.Policies()
}

// TaskKey builds the composite key used to correlate approvals with actions.
// It is exported so the exec sub-package can build the same keys.
func TaskKey(agentID, resource, action string) string {
	return agentID + "|" + resource + "|" + action
}

// splitTaskKey parses a composite key back into agentID, resource and action.
func splitTaskKey(key string) (agentID, resource, action string) {
	parts := strings.SplitN(key, "|", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

// publish delivers an event to the optional bus. A nil bus is a no-op so the
// firewall keeps working unchanged when no bus is attached.
func (f *Firewall) publish(ev eventbus.Event) {
	if f.bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = "governance"
	}
	f.bus.Publish(ev)
}

// Check evaluates whether an agent can perform an action. It returns whether
// the action is allowed, the risk assessment, and — when the action needs human
// approval — the pending approval (non-nil) alongside allowed=false and a nil
// error. Errors are reserved for denials (unknown agent, missing permission, or
// an always-blocked action). The flow: verify the agent, check its permission,
// assess risk, create a pending approval if required, deny always-blocked
// CRITICAL actions, record the decision to the audit log.
func (f *Firewall) Check(agentID, resource, action string) (allowed bool, risk domain.Risk, approval *domain.Approval, err error) {
	start := time.Now()
	defer func() { metrics.Default().RecordPolicyEval(time.Since(start)) }()

	// 1. Authentication.
	agent, ok := f.agents[agentID]
	if !ok {
		r := domain.Risk{Level: domain.RiskCritical, Score: 1.0, Factors: []string{"unknown agent"}, Mitigation: "register the agent before use"}
		f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "denied"})
		f.publish(eventbus.Event{Kind: eventbus.PolicyBlocked, Subject: resource, Payload: map[string]string{"action": action, "reason": "unknown agent"}})
		return false, r, nil, fmt.Errorf("governance: unknown agent %q", agentID)
	}

	// 2. Authorization.
	if !agent.Can(resource, action) {
		r := f.assessor.AssessAction(resource, action)
		f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "denied"})
		f.publish(eventbus.Event{Kind: eventbus.PolicyBlocked, Subject: resource, Payload: map[string]string{"action": action, "reason": "lacks permission"}})
		return false, r, nil, fmt.Errorf("governance: agent %q lacks permission %q:%q", agentID, resource, action)
	}

	// 3. Risk scoring.
	r := f.assessor.AssessAction(resource, action)
	f.publish(eventbus.Event{Kind: eventbus.PolicyEvaluated, Subject: resource, Payload: map[string]string{"action": action, "level": string(r.Level)}})

	// 5. Always-blocked CRITICAL actions.
	if r.Level == domain.RiskCritical && action == "drop" {
		f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "blocked"})
		f.publish(eventbus.Event{Kind: eventbus.PolicyBlocked, Subject: resource, Payload: map[string]string{"action": action, "reason": "always blocked"}})
		return false, r, nil, fmt.Errorf("governance: %s:%s is always blocked", resource, action)
	}

	// 4. Approval gate.
	if appr.RequiresApproval(r.Level) {
		key := TaskKey(agentID, resource, action)
		if !f.approvedKeys[key] {
			appr := f.approval.Request(key, agentID, r.Mitigation)
			f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "pending"})
			f.publish(eventbus.Event{Kind: eventbus.ApprovalRequested, Subject: appr.ID, Payload: map[string]string{"resource": resource, "action": action}})
			metrics.Default().RecordApproval()
			return false, r, &appr, nil
		}
		// A granted approval authorizes exactly one action: consume it so it
		// cannot be reused on subsequent Checks.
		delete(f.approvedKeys, key)
	}

	// 7. Allowed.
	f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Approved: true, Result: "allowed"})
	return true, r, nil, nil
}

// ApproveAction approves a previously-requested approval by ID. On success it
// records the decision in the audit log so that a subsequent Check for the same
// action passes.
func (f *Firewall) ApproveAction(approvalID, approver string) error {
	appr, err := f.approval.Approve(approvalID, approver)
	if err != nil {
		return err
	}
	agentID, resource, action := splitTaskKey(appr.TaskID)
	f.approvedKeys[appr.TaskID] = true
	risk := domain.Risk{Level: domain.RiskHigh, Score: 0.75, Factors: []string{"approved by human"}, Mitigation: "human approval granted"}
	f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: risk, Approved: true, Result: "approved"})
	metrics.Default().RecordApproval()
	return nil
}

// RejectAction rejects a previously-requested approval by ID. The action stays
// denied; a later Check for the same action will require a fresh approval.
func (f *Firewall) RejectAction(approvalID, approver, reason string) error {
	appr, err := f.approval.Reject(approvalID, approver, reason)
	if err != nil {
		return err
	}
	agentID, resource, action := splitTaskKey(appr.TaskID)
	delete(f.approvedKeys, appr.TaskID)
	risk := domain.Risk{Level: domain.RiskHigh, Score: 0.75, Factors: []string{"rejected by human"}, Mitigation: "human approval denied"}
	f.audit.Record(audit.AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: risk, Result: "denied"})
	metrics.Default().RecordApproval()
	return nil
}

// AuditLog returns the firewall's audit log for inspection.
func (f *Firewall) AuditLog() *audit.AuditLog {
	return f.audit
}
