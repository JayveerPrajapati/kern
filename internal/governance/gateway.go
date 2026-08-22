package governance

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
)

// ToolGateway is the unified gateway that every governed tool call passes
// through. Strict Plan Phase 7 P0: "All governed operations pass through one
// gateway."
//
// It evaluates: agent identity, task boundary, resource, action, permission,
// risk, policy, and approval — then allows or denies. Every evaluation is
// logged to the audit log.
type ToolGateway struct {
	firewall *firewall.Firewall
	audit    func(domain.TaskState, string, string) // optional audit callback
}

// NewToolGateway wraps a firewall with task-boundary and budget enforcement.
func NewToolGateway(fw *firewall.Firewall) *ToolGateway {
	return &ToolGateway{firewall: fw}
}

// Evaluate checks whether a tool call is allowed. It evaluates:
//  1. Task boundary (is the resource path within the task's allowed paths?)
//  2. Firewall policy (agent + resource + action → allowed/risk/approval)
//  3. Safety budget (has the task exceeded its limits?)
//
// Returns: allowed, risk, approval, error (error when denied with reason).
func (g *ToolGateway) Evaluate(agentID, taskID, resource, action string, boundary domain.TaskBoundary, budget *domain.SafetyBudget) (bool, domain.Risk, *domain.Approval, error) {
	// 1. Task boundary check.
	if !boundary.CheckPath(resource) {
		err := fmt.Errorf("tool gateway: resource %q is outside task %s boundary (denied paths: %v)", resource, taskID, boundary.DeniedPaths)
		g.logAudit(taskID, "DENY", err.Error())
		return false, domain.Risk{Level: domain.RiskCritical, Blocked: true}, nil, err
	}

	// 2. Firewall policy check.
	allowed, risk, approval, fwErr := g.firewall.Check(agentID, resource, action)
	if fwErr != nil {
		g.logAudit(taskID, "DENY", fwErr.Error())
		return false, risk, nil, fmt.Errorf("tool gateway: firewall denied: %w", fwErr)
	}
	if !allowed {
		g.logAudit(taskID, "DENY", "firewall policy denied")
		return false, risk, nil, fmt.Errorf("tool gateway: firewall policy denied for agent=%s resource=%s action=%s", agentID, resource, action)
	}

	// 3. Safety budget check.
	if budget != nil {
		if exceeded, reason := budget.Exceeded(); exceeded {
			err := fmt.Errorf("tool gateway: safety budget exceeded: %s", reason)
			g.logAudit(taskID, "PAUSE", err.Error())
			return false, risk, nil, err
		}
	}

	g.logAudit(taskID, "ALLOW", "agent="+agentID+" resource="+resource+" action="+action)
	return true, risk, approval, nil
}

// logAudit records the gateway decision. When no audit callback is set, it's
// a no-op.
func (g *ToolGateway) logAudit(taskID, decision, detail string) {
	if g.audit != nil {
		g.audit(domain.TaskState(""), decision, detail)
	}
}

// WithAudit sets an audit callback for logging gateway decisions.
func (g *ToolGateway) WithAudit(fn func(domain.TaskState, string, string)) *ToolGateway {
	g.audit = fn
	return g
}
