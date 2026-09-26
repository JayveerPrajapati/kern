// Package firewall provides the unified AI change firewall that ties together
// agent identity, risk scoring, the approval workflow, and the audit log.

package governance

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// Firewall is the unified AI change firewall. Every agent action passes through
// it in the order: Authentication → Authorization → Permission → Risk → Impact
// → Policy → Approval → Execution. It fails closed: unknown agents, missing
// permissions, and always-blocked actions are denied by default.
type Firewall struct {
	mu           sync.RWMutex
	agents       map[string]*AgentIdentity
	assessor     *RiskAssessor
	approval     *ApprovalWorkflow
	audit        *AuditLog
	approvedKeys map[string]bool
	bus          *eventbus.Bus // optional event publisher; nil = no-op
	egress       EgressRule    // outbound-connection posture (zero value = local-only default)
	execCommand  string        // optional command text bound to command.execute approvals (WithExecCommand)
}

// NewFirewall creates a new change firewall with the default policies. No
// agents are registered; call WithAgents to add them. Approvals live in memory
// and do not survive a restart.
func NewFirewall() *Firewall {
	return &Firewall{
		agents:       map[string]*AgentIdentity{},
		assessor:     NewRiskAssessor(DefaultPolicies()),
		approval:     NewApprovalWorkflow(),
		audit:        NewAuditLog(),
		approvedKeys: map[string]bool{},
	}
}

// NewFirewallWithApprovalStore creates a change firewall whose approval
// workflow is backed by the project's approval file (<root>/.kern/
// approvals.json): approvals requested by Check survive restarts and can be
// resolved out-of-band (`kern approve <id>` or the web UI), and pending
// approvals from a previous process are restored on construction. An empty
// root falls back to the in-memory firewall.
func NewFirewallWithApprovalStore(root string) *Firewall {
	return &Firewall{
		agents:       map[string]*AgentIdentity{},
		assessor:     NewRiskAssessor(DefaultPolicies()),
		approval:     NewPersistedApprovalWorkflow(root),
		audit:        NewAuditLog(),
		approvedKeys: map[string]bool{},
	}
}

// WithAgents registers agents that can act through the firewall. It returns
// the firewall for chaining.
func (f *Firewall) WithAgents(agents ...*AgentIdentity) *Firewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range agents {
		if a != nil {
			f.agents[a.ID] = a
		}
	}
	return f
}

// WithPolicies sets custom risk policies (overrides the defaults). It returns
// the firewall for chaining. The assessor swap is guarded by f.mu so a
// concurrent Policies() read never sees a torn pointer.
func (f *Firewall) WithPolicies(policies []domain.Policy) *Firewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assessor = NewRiskAssessor(policies)
	return f
}

// WithEgressRule configures the outbound-connection posture for egress
// actions. It returns the firewall for chaining. An unset rule (zero value)
// leaves the default local-only posture in place.
func (f *Firewall) WithEgressRule(rule EgressRule) *Firewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.egress = rule
	return f
}

// WithBus attaches an optional event bus. When non-nil, the firewall publishes
// policy.evaluated, policy.blocked and approval.requested events at the
// relevant transition points. A nil bus is a no-op (firewall still works).
// The swap is guarded by f.mu so concurrent publish reads never race it.
func (f *Firewall) WithBus(b *eventbus.Bus) *Firewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bus = b
	return f
}

// WithExecCommand binds the firewall's approval gate to a concrete command
// text (audit A3: exec approvals must be command-specific). When a
// command.execute Check hits the approval gate, the approval key gains a
// SHA-256 of the command (so one approval authorizes exactly that command)
// and the persisted approval records the command text as evidence. It returns
// the firewall for chaining; an empty command leaves the gate unbound.
func (f *Firewall) WithExecCommand(command string) *Firewall {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.execCommand = command
	return f
}

// grantApproval marks a task key as approved for subsequent Checks on this
// firewall instance (single use — the grant is consumed by the next matching
// Check). It lets the exec gate honor an approval that was decided
// out-of-band (`kern approve <id>` persisted the decision while this process
// was running) without waiting for an in-process ApproveAction.
func (f *Firewall) grantApproval(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvedKeys[key] = true
}

// Policies returns a copy of the risk policies currently loaded. It is the
// additive accessor callers (e.g. the context engine) use to surface the
// governance rules that apply to a change scope. The assessor read is guarded
// by f.mu so a concurrent WithPolicies swap is never observed torn.
func (f *Firewall) Policies() []domain.Policy {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.assessor.Policies()
}

// TaskKey builds the composite key used to correlate approvals with actions.
// It is exported so the exec sub-package can build the same keys.
//
// The key is a lossless encoding of the (agentID, resource, action) triple:
// '|' is the field separator, so components are percent-escaped on compose
// ('|' → "%7C", '%' → "%25") and unescaped on split. This keeps resources that
// themselves contain '|' (e.g. "db|prod|primary") round-tripping exactly while
// the full composite key stays unique per triple and is what enforcement
// (approvedKeys) and the approval store use.
func TaskKey(agentID, resource, action string) string {
	return escapeKeyComponent(agentID) + "|" + escapeKeyComponent(resource) + "|" + escapeKeyComponent(action)
}

// escapeKeyComponent percent-escapes the field separator ('|') and the escape
// character itself ('%') inside a task-key component. '%' must be escaped
// first so an already-escaped sequence can never be confused with a literal.
func escapeKeyComponent(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	return strings.ReplaceAll(s, "|", "%7C")
}

// unescapeKeyComponent is the inverse of escapeKeyComponent. "%7C" is
// unescaped before "%25" so a literal "|" in the original component wins over
// a literal "%7C" (which the round-trip would have escaped to "%257C").
func unescapeKeyComponent(s string) string {
	s = strings.ReplaceAll(s, "%7C", "|")
	return strings.ReplaceAll(s, "%25", "%")
}

// splitTaskKey parses a composite key back into agentID, resource and action.
// It is the exact inverse of TaskKey: escaped components are unescaped, so a
// resource containing '|' round-trips losslessly. A malformed key (not
// exactly three fields) yields empty components.
func splitTaskKey(key string) (agentID, resource, action string) {
	parts := strings.SplitN(key, "|", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return unescapeKeyComponent(parts[0]), unescapeKeyComponent(parts[1]), unescapeKeyComponent(parts[2])
}

// publish delivers an event to the optional bus. A nil bus is a no-op so the
// firewall keeps working unchanged when no bus is attached. The bus read is
// guarded by f.mu (publish is never called with f.mu held by Check paths) so
// a concurrent WithBus swap is never observed torn.
func (f *Firewall) publish(ev eventbus.Event) {
	f.mu.RLock()
	bus := f.bus
	f.mu.RUnlock()
	if bus == nil {
		return
	}
	if ev.Source == "" {
		ev.Source = "governance"
	}
	bus.Publish(ev)
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
	f.mu.RLock()
	agent, ok := f.agents[agentID]
	f.mu.RUnlock()
	if !ok {
		r := domain.Risk{Level: domain.RiskCritical, Score: 1.0, Factors: []string{"unknown agent"}, Mitigation: "register the agent before use", Blocked: true}
		f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "denied"})
		f.publish(eventbus.Event{Kind: eventbus.PolicyBlocked, Subject: resource, Payload: map[string]string{"action": action, "reason": "unknown agent"}})
		return false, r, nil, fmt.Errorf("governance: unknown agent %q", agentID)
	}

	// 2. Authorization.
	if !agent.Can(resource, action) {
		r := f.assessor.AssessAction(resource, action)
		r.Blocked = true
		f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "denied"})
		f.publish(eventbus.Event{Kind: eventbus.PolicyBlocked, Subject: resource, Payload: map[string]string{"action": action, "reason": "lacks permission"}})
		return false, r, nil, fmt.Errorf("governance: agent %q lacks permission %q:%q", agentID, resource, action)
	}

	// 3. Risk scoring.
	r := f.assessor.AssessAction(resource, action)
	f.publish(eventbus.Event{Kind: eventbus.PolicyEvaluated, Subject: resource, Payload: map[string]string{"action": action, "level": string(r.Level)}})

	// 3.5 Egress policy (P0-006): outbound connections ("egress" actions) are
	// governed by the configured egress rule; an unset rule defaults to
	// local-only. Denied targets fail closed; external-approved targets defer
	// to the approval gate below.
	if action == "egress" {
		f.mu.RLock()
		rule := f.egress
		f.mu.RUnlock()
		if rule.Policy == "" {
			rule = EgressRule{Policy: EgressLocalOnly}
		}
		host, port := parseEgressTarget(resource)
		dec := CheckEgress(rule, host, port)
		if !dec.Allowed && !dec.RequiresApproval {
			r.Blocked = true
			RecordDecision(f.audit, f.bus, agentID, resource, action, PolicyDecision{Policy: "egress", Decision: "denied", Reason: dec.Reason}, r)
			return false, r, nil, fmt.Errorf("governance: %s:%s blocked by egress policy %q: %s", resource, action, rule.Policy, dec.Reason)
		}
		if dec.RequiresApproval {
			r.ApprovalRequired = true
		}
	}

	// 5. Always-blocked CRITICAL actions.
	if r.Level == domain.RiskCritical && action == "drop" {
		r.Blocked = true
		f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "blocked"})
		f.publish(eventbus.Event{Kind: eventbus.PolicyBlocked, Subject: resource, Payload: map[string]string{"action": action, "reason": "always blocked"}})
		return false, r, nil, fmt.Errorf("governance: %s:%s is always blocked", resource, action)
	}

	// 4. Approval gate.
	if r.ApprovalRequired || RequiresApproval(r.Level) {
		key := TaskKey(agentID, resource, action)
		cmd := ""
		if resource == "command" && action == "execute" {
			if c := f.execCommand; c != "" {
				// Bind the approval to the exact command text (audit A3): the
				// key gains a SHA-256 of the command so one approval
				// authorizes one command, and the persisted record carries
				// the command text as evidence.
				key = execCommandKey(c)
				cmd = c
			}
		}
		f.mu.Lock()
		approved := f.approvedKeys[key]
		if approved {
			// A granted approval authorizes exactly one action: consume it so
			// it cannot be reused on subsequent Checks.
			delete(f.approvedKeys, key)
		}
		f.mu.Unlock()

		if !approved {
			var evidence []string
			if cmd != "" {
				evidence = []string{cmd}
			}
			appr, err := f.approval.RequestWithBinding(key, agentID, r.Mitigation, r.Level, nil, evidence, "")
			if err != nil {
				f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "denied"})
				return false, r, nil, fmt.Errorf("governance: approval for %s:%s could not be persisted: %w", resource, action, err)
			}
			f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Result: "pending"})
			f.publish(eventbus.Event{Kind: eventbus.ApprovalRequested, Subject: appr.ID, Payload: map[string]string{"resource": resource, "action": action, "risk_level": string(r.Level)}})
			metrics.Default().RecordApproval()
			return false, r, &appr, nil
		}
	}

	// 7. Allowed.
	f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: r, Approved: true, Result: "allowed"})
	return true, r, nil, nil
}

// CheckEgress evaluates whether an agent may open an outbound connection to
// host:port. Authentication and authorization mirror Check (resource
// "egress", action "connect"); the configured egress rule (default
// local-only when unset) makes the decision; external-approved external
// targets go through the approval workflow; every outcome is recorded in the
// audit log and published on the bus.
func (f *Firewall) CheckEgress(agentID, host string, port int) (allowed bool, dec EgressDecision, approval *domain.Approval, err error) {
	start := time.Now()
	defer func() { metrics.Default().RecordPolicyEval(time.Since(start)) }()

	// 1. Authentication.
	f.mu.RLock()
	agent, ok := f.agents[agentID]
	f.mu.RUnlock()
	if !ok {
		r := domain.Risk{Level: domain.RiskCritical, Score: 1.0, Factors: []string{"unknown agent"}, Mitigation: "register the agent before use", Blocked: true}
		RecordDecision(f.audit, f.bus, agentID, "egress", "connect", PolicyDecision{Policy: "egress", Decision: "denied", Reason: "unknown agent"}, r)
		return false, dec, nil, fmt.Errorf("governance: unknown agent %q", agentID)
	}

	// 2. Authorization.
	if !agent.Can("egress", "connect") {
		r := f.assessor.AssessAction("egress", "connect")
		r.Blocked = true
		RecordDecision(f.audit, f.bus, agentID, "egress", "connect", PolicyDecision{Policy: "egress", Decision: "denied", Reason: "lacks permission"}, r)
		return false, dec, nil, fmt.Errorf("governance: agent %q lacks permission %q:%q", agentID, "egress", "connect")
	}

	// 3. Rule resolution and decision.
	r := f.assessor.AssessAction("egress", "connect")
	f.mu.RLock()
	rule := f.egress
	f.mu.RUnlock()
	if rule.Policy == "" {
		rule = EgressRule{Policy: EgressLocalOnly}
	}
	dec = CheckEgress(rule, host, port)

	// 4. Approval gate for external-approved targets.
	if dec.RequiresApproval {
		key := TaskKey(agentID, "egress", fmt.Sprintf("%s:%d", host, port))
		f.mu.Lock()
		approved := f.approvedKeys[key]
		if approved {
			// A granted approval authorizes exactly one connection: consume it
			// so it cannot be reused on subsequent CheckEgress calls.
			delete(f.approvedKeys, key)
		}
		f.mu.Unlock()
		if !approved {
			// The egress approval records the exact connection it authorizes
			// as evidence (audit A3: approvals carry the content they gate).
			appr, err := f.approval.RequestWithBinding(key, agentID, dec.Reason, domain.RiskHigh, nil, []string{fmt.Sprintf("%s:%d", host, port)}, "")
			if err != nil {
				return false, dec, nil, fmt.Errorf("governance: approval for egress %s:%d could not be persisted: %w", host, port, err)
			}
			f.publish(eventbus.Event{Kind: eventbus.ApprovalRequested, Subject: appr.ID, Payload: map[string]string{"host": host, "port": fmt.Sprintf("%d", port), "policy": string(rule.Policy), "risk_level": string(domain.RiskHigh)}})
			RecordDecision(f.audit, f.bus, agentID, host, "connect", PolicyDecision{Policy: "egress", Decision: "pending", Reason: dec.Reason}, r)
			metrics.Default().RecordApproval()
			return false, dec, &appr, nil
		}
		// Approved: reflect the grant in the decision and fall through to the
		// allowed branch.
		dec = EgressDecision{Policy: rule.Policy, Allowed: true, Reason: "approved by human"}
	}

	// 5. Allowed / denied.
	if dec.Allowed {
		RecordDecision(f.audit, f.bus, agentID, host, "connect", PolicyDecision{Policy: "egress", Decision: "allowed", Reason: dec.Reason}, r)
		return true, dec, nil, nil
	}
	RecordDecision(f.audit, f.bus, agentID, host, "connect", PolicyDecision{Policy: "egress", Decision: "denied", Reason: dec.Reason}, r)
	return false, dec, nil, fmt.Errorf("governance: egress to %s:%d blocked by policy %q: %s", host, port, rule.Policy, dec.Reason)
}

// ApproveAction approves a previously-requested approval by ID. On success it
// records the decision in the audit log so that a subsequent Check for the same
// action passes.
//
// Trust model: approvals are local-operator actions, not remotely grantable
// ones. The web console — the only remote-reachable approval surface — now
// requires a loopback bind or KERN_AUTH_TOKEN (`kern serve` refuses
// non-loopback binds without the token, and enterprise fails closed with 503;
// added 2026-09-25), so the approval workflow is not remotely satisfiable by
// default. The approver string is a self-attested local identity recorded for
// audit, not an authentication credential.
func (f *Firewall) ApproveAction(approvalID, approver string) error {
	appr, err := f.approval.Approve(approvalID, approver)
	if err != nil {
		return err
	}
	agentID, resource, action := splitTaskKey(appr.TaskID)
	f.mu.Lock()
	f.approvedKeys[appr.TaskID] = true
	f.mu.Unlock()
	risk := domain.Risk{Level: domain.RiskHigh, Score: 0.75, Factors: []string{"approved by human"}, Mitigation: "human approval granted"}
	f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: risk, Approved: true, Result: "approved"})
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
	f.mu.Lock()
	delete(f.approvedKeys, appr.TaskID)
	f.mu.Unlock()
	risk := domain.Risk{Level: domain.RiskHigh, Score: 0.75, Factors: []string{"rejected by human"}, Mitigation: "human approval denied"}
	f.audit.Record(AuditEntry{AgentID: agentID, Action: action, Resource: resource, Risk: risk, Result: "denied"})
	metrics.Default().RecordApproval()
	return nil
}

// AuditLog returns the firewall's audit log for inspection.
func (f *Firewall) AuditLog() *AuditLog {
	return f.audit
}
