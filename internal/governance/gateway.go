package governance

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ToolGateway is the unified gateway that every governed tool call passes
// through. : "All governed operations pass through one
// gateway."
// It evaluates: agent identity, task boundary, resource, action, permission,
// risk, policy, and approval — then allows or denies. Every evaluation is
// logged to the audit log.
type ToolGateway struct {
	firewall *Firewall
	audit    func(domain.TaskState, string, string) // optional audit callback
}

// NewToolGateway wraps a firewall with task-boundary and budget enforcement.
func NewToolGateway(fw *Firewall) *ToolGateway {
	return &ToolGateway{firewall: fw}
}

// Evaluate checks whether a tool call is allowed. It evaluates:
// 1. Task boundary (is the resource path within the task's allowed paths?)
// 2. Firewall policy (agent + resource + action → allowed/risk/approval)
// 3. Safety budget (has the task exceeded its limits?)
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

// EvaluateScoped is the unified-scoped gateway entry (P7.3). It takes a
// TaskScope (paths + envs + services) instead of a bare TaskBoundary, runs the
// same path/firewall/budget pipeline, and returns a structured GatewayResult
// with an explain-deny object (P7.6) so callers never parse error strings.
// It is a thin wrapper over EvaluateScopedFull that passes empty service and
// artifact dimensions, so its behavior is unchanged and backward-compatible:
// when scope.Services and scope.Artifacts are empty, those gates are all-pass.
func (g *ToolGateway) EvaluateScoped(agentID, taskID, resource, action, env string, scope domain.TaskScope, budget *domain.SafetyBudget) domain.GatewayResult {
	return g.EvaluateScopedFull(agentID, taskID, resource, action, "", "", env, scope, budget)
}

// EvaluateScopedFull is the unified-scoped gateway entry that additionally
// enforces the service and artifact scope dimensions (P7.3: "the same task
// policy applies to tools, artifacts, runtime"). It runs, in order:
// env gate → service gate → artifact gate → path gate → firewall → budget
// and returns a structured GatewayResult with an explain-deny object (P7.6).
// The service and artifact gates are all-pass when scope.Services /
// scope.Artifacts are empty.
func (g *ToolGateway) EvaluateScopedFull(agentID, taskID, resource, action, service, artifact, env string, scope domain.TaskScope, budget *domain.SafetyBudget) domain.GatewayResult {
	// Env gate from the unified scope.
	if !scope.CheckEnv(env) {
		deny := &domain.DenyReason{
			Stage: "env", AgentID: agentID, TaskID: taskID, Resource: resource, Action: action,
			Reason: "environment " + env + " is outside the task scope",
			Risk:   domain.Risk{Level: domain.RiskCritical, Blocked: true},
			Policy: "scope.env", SafeAlternative: "operate in an allowed environment from scope.Envs",
		}
		g.logAudit(taskID, "DENY", deny.Reason)
		return domain.GatewayResult{Decision: domain.DecisionDenied, Risk: deny.Risk, Deny: deny, Budget: budget}
	}
	// Service gate from the unified scope.
	if !scope.CheckService(service) {
		deny := &domain.DenyReason{
			Stage: "service", AgentID: agentID, TaskID: taskID, Resource: resource, Action: action,
			Reason: "service " + service + " is outside the task scope",
			Risk:   domain.Risk{Level: domain.RiskCritical, Blocked: true},
			Policy: "scope.service", SafeAlternative: "operate on an allowed service from scope.Services",
		}
		g.logAudit(taskID, "DENY", deny.Reason)
		return domain.GatewayResult{Decision: domain.DecisionDenied, Risk: deny.Risk, Deny: deny, Budget: budget}
	}
	// Artifact gate from the unified scope.
	if !scope.CheckArtifact(artifact) {
		deny := &domain.DenyReason{
			Stage: "artifact", AgentID: agentID, TaskID: taskID, Resource: resource, Action: action,
			Reason: "artifact kind " + artifact + " is outside the task scope",
			Risk:   domain.Risk{Level: domain.RiskCritical, Blocked: true},
			Policy: "scope.artifact", SafeAlternative: "produce an allowed artifact kind from scope.Artifacts",
		}
		g.logAudit(taskID, "DENY", deny.Reason)
		return domain.GatewayResult{Decision: domain.DecisionDenied, Risk: deny.Risk, Deny: deny, Budget: budget}
	}
	// Path gate from the unified scope.
	if !scope.CheckPath(resource) {
		deny := &domain.DenyReason{
			Stage: "boundary", AgentID: agentID, TaskID: taskID, Resource: resource, Action: action,
			Reason: "resource " + resource + " is outside the task scope",
			Risk:   domain.Risk{Level: domain.RiskCritical, Blocked: true},
			Policy: "scope.path", SafeAlternative: "narrow the change to an allowed path",
		}
		g.logAudit(taskID, "DENY", deny.Reason)
		return domain.GatewayResult{Decision: domain.DecisionDenied, Risk: deny.Risk, Deny: deny, Budget: budget}
	}
	// Firewall policy gate.
	allowed, risk, approval, fwErr := g.firewall.Check(agentID, resource, action)
	if fwErr != nil || !allowed {
		reason := "firewall policy denied"
		if fwErr != nil {
			reason = fwErr.Error()
		}
		deny := &domain.DenyReason{
			Stage: "firewall", AgentID: agentID, TaskID: taskID, Resource: resource, Action: action,
			Reason: reason, Risk: risk, RequiredApproval: approval,
			Policy: "firewall.permission", SafeAlternative: "request the required permission or obtain approval",
		}
		g.logAudit(taskID, "DENY", reason)
		return domain.GatewayResult{Decision: domain.DecisionDenied, Risk: risk, Approval: approval, Deny: deny, Budget: budget}
	}
	// Budget gate -> PAUSE.
	if budget != nil {
		if exceeded, reason := budget.Exceeded(); exceeded {
			deny := &domain.DenyReason{
				Stage: "budget", AgentID: agentID, TaskID: taskID, Resource: resource, Action: action,
				Reason: "safety budget exceeded: " + reason, Risk: risk,
				Policy: "safety_budget", SafeAlternative: "reduce resource usage or increase the budget limit",
			}
			g.logAudit(taskID, "PAUSE", deny.Reason)
			return domain.GatewayResult{Decision: domain.DecisionPaused, Risk: risk, Deny: deny, Budget: budget}
		}
	}
	g.logAudit(taskID, "ALLOW", "agent="+agentID+" resource="+resource+" action="+action)
	return domain.GatewayResult{Decision: domain.DecisionAllowed, Allowed: true, Risk: risk, Approval: approval, Budget: budget}
}

// DryRun evaluates a governed call WITHOUT committing it (P7.5). It returns the
// same GatewayResult the live EvaluateScoped path would produce, so a caller
// can predict whether a call would be allowed, denied, paused, or require
// approval before running it. The budget is probed on a clone so dry runs never
// advance live usage.
func (g *ToolGateway) DryRun(agentID, taskID, resource, action, env string, scope domain.TaskScope, budget *domain.SafetyBudget) domain.GatewayResult {
	var probe *domain.SafetyBudget
	if budget != nil {
		c := *budget
		probe = &c
	}
	return g.EvaluateScoped(agentID, taskID, resource, action, env, scope, probe)
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
