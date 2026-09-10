// Package governance policy decisions: a uniform audit+event record for the
// outcome of a single policy evaluation.
package governance

import (
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// PolicyDecision records the outcome of a single policy evaluation: which
// policy decided, what it decided, and why.
type PolicyDecision struct {
	Policy   string // policy name: "firewall", "permission", "egress", ...
	Decision string // "allowed" | "denied" | "pending" | "approved" | "rejected"
	Reason   string // human-readable justification
}

// RecordDecision writes a policy decision to the audit log and, when a bus is
// attached, publishes the matching policy event (PolicyEvaluated for
// allowed/pending, PolicyBlocked for denied/blocked — mirroring the existing
// Check conventions). The audit entry carries Result=Decision and the new
// Policy/Reason fields. Never fails (mirrors AuditLog.Record).
func RecordDecision(l *AuditLog, bus *eventbus.Bus, agentID, resource, action string, d PolicyDecision, risk domain.Risk) {
	l.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: risk, Result: d.Decision, Policy: d.Policy, Reason: d.Reason})
	if bus == nil {
		return
	}
	kind := eventbus.PolicyEvaluated
	if d.Decision == "denied" || d.Decision == "blocked" {
		kind = eventbus.PolicyBlocked
	}
	ev := eventbus.Event{Kind: kind, Subject: resource, Payload: map[string]string{"action": action, "policy": d.Policy, "decision": d.Decision, "reason": d.Reason}}
	if ev.Source == "" {
		ev.Source = "governance"
	}
	bus.Publish(ev)
}
