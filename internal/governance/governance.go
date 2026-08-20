// Package governance is a facade that re-exports the governance sub-packages
// (identity, approval, audit, risk, exec, firewall) via type aliases so that
// existing importers of internal/governance continue to work unchanged. New
// code may import the sub-packages directly.
package governance

import (
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/approval"
	"github.com/JayveerPrajapati/kern/internal/governance/audit"
	"github.com/JayveerPrajapati/kern/internal/governance/exec"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
	"github.com/JayveerPrajapati/kern/internal/governance/identity"
	"github.com/JayveerPrajapati/kern/internal/governance/risk"
)

// Type aliases preserve backward compatibility for all existing importers.
type Permission = identity.Permission
type AgentIdentity = identity.AgentIdentity
type ApprovalWorkflow = approval.ApprovalWorkflow
type AuditEntry = audit.AuditEntry
type AuditLog = audit.AuditLog
type RiskAssessor = risk.RiskAssessor
type Firewall = firewall.Firewall

// identity package
func NewAgent(id, name, agentType string, perms []Permission) *AgentIdentity {
	return identity.NewAgent(id, name, agentType, perms)
}

func RegisterAgent(a *AgentIdentity) error {
	return identity.RegisterAgent(a)
}

func GetAgent(id string) (*AgentIdentity, error) {
	return identity.GetAgent(id)
}

// approval package
func NewApprovalWorkflow() *ApprovalWorkflow {
	return approval.NewApprovalWorkflow()
}

func RequiresApproval(level domain.RiskLevel) bool {
	return approval.RequiresApproval(level)
}

// audit package
func NewAuditLog() *AuditLog {
	return audit.NewAuditLog()
}

// risk package
func NewRiskAssessor(policies []domain.Policy) *RiskAssessor {
	return risk.NewRiskAssessor(policies)
}

func DefaultPolicies() []domain.Policy {
	return risk.DefaultPolicies()
}

// firewall package
func NewFirewall() *Firewall {
	return firewall.NewFirewall()
}

// TaskKey builds the composite approval-key used by the firewall and the exec
// sub-package.
func TaskKey(agentID, resource, action string) string {
	return firewall.TaskKey(agentID, resource, action)
}

// exec package
func CheckExec(toolName ...string) error {
	return exec.CheckExec(toolName...)
}

func RequestExecApproval(wf *ApprovalWorkflow, toolName ...string) (*domain.Approval, domain.Risk, error) {
	return exec.RequestExecApproval(wf, toolName...)
}

func ResumeExecApproval(wf *ApprovalWorkflow, approvalID string) error {
	return exec.ResumeExecApproval(wf, approvalID)
}
