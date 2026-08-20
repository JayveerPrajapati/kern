package governance

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// broadAgent has permissions on every resource the tests exercise.
func broadAgent(id string) *AgentIdentity {
	return NewAgent(id, "Agent", "coder", []Permission{
		{Resource: "source", Action: "write"},
		{Resource: "tests", Action: "write"},
		{Resource: "documentation", Action: "write"},
		{Resource: "security", Action: "write"},
		{Resource: "production", Action: "deploy"},
		{Resource: "database", Action: "drop"},
		{Resource: "config", Action: "write"},
	})
}

func TestFirewallLowRiskAllowedNoApproval(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-low"))
	allowed, risk, approval, err := f.Check("agent-low", "tests", "write")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !allowed {
		t.Error("LOW risk tests:write should be allowed")
	}
	if risk.Level != domain.RiskLow {
		t.Errorf("risk level = %s, want LOW", risk.Level)
	}
	if approval != nil {
		t.Errorf("approval should be nil, got %+v", approval)
	}
}

func TestFirewallMediumRiskAllowedNoApproval(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-medium"))
	allowed, risk, approval, err := f.Check("agent-medium", "source", "write")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !allowed {
		t.Error("MEDIUM risk source:write should be allowed")
	}
	if risk.Level != domain.RiskMedium {
		t.Errorf("risk level = %s, want MEDIUM", risk.Level)
	}
	if approval != nil {
		t.Errorf("approval should be nil, got %+v", approval)
	}
}

func TestFirewallHighRiskRequiresApproval(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-security"))
	allowed, risk, approval, err := f.Check("agent-security", "security", "write")
	if err != nil {
		t.Fatalf("Check should not error for a pending approval, got: %v", err)
	}
	if allowed {
		t.Error("HIGH risk action should be denied without approval")
	}
	if risk.Level != domain.RiskHigh {
		t.Errorf("risk level = %s, want HIGH", risk.Level)
	}
	if approval == nil {
		t.Fatal("pending approval should be returned")
	}
	if approval.Status != "pending" {
		t.Errorf("approval status = %s, want pending", approval.Status)
	}
}

func TestFirewallCriticalDeployRequiresApproval(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-prod"))
	allowed, risk, approval, err := f.Check("agent-prod", "production", "deploy")
	if err != nil {
		t.Fatalf("Check should not error for a pending approval, got: %v", err)
	}
	if allowed {
		t.Error("CRITICAL production:deploy should not be allowed without approval")
	}
	if risk.Level != domain.RiskCritical {
		t.Errorf("risk level = %s, want CRITICAL", risk.Level)
	}
	if approval == nil {
		t.Fatal("pending approval should be returned")
	}
}

func TestFirewallCriticalDatabaseDropAlwaysBlocked(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-drop"))
	allowed, risk, _, err := f.Check("agent-drop", "database", "drop")
	if err == nil {
		t.Fatal("database:drop should be blocked with an error")
	}
	if allowed {
		t.Error("database:drop should never be allowed")
	}
	if risk.Level != domain.RiskCritical {
		t.Errorf("risk level = %s, want CRITICAL", risk.Level)
	}
	if !strings.Contains(err.Error(), "always blocked") {
		t.Errorf("error should mention always blocked, got %q", err.Error())
	}
}

func TestFirewallAgentWithoutPermissionDenied(t *testing.T) {
	limited := NewAgent("limited", "Limited", "coder", []Permission{
		{Resource: "source", Action: "write"},
	})
	f := NewFirewall().WithAgents(limited)
	allowed, _, _, err := f.Check("limited", "production", "deploy")
	if err == nil {
		t.Fatal("Check should error for a missing permission")
	}
	if allowed {
		t.Error("action without permission should be denied")
	}
	if !strings.Contains(err.Error(), "lacks permission") {
		t.Errorf("error should mention lacks permission, got %q", err.Error())
	}
}

func TestFirewallUnknownAgentDenied(t *testing.T) {
	f := NewFirewall()
	allowed, _, _, err := f.Check("ghost", "tests", "write")
	if err == nil {
		t.Fatal("Check should error for an unknown agent")
	}
	if allowed {
		t.Error("unknown agent should be denied")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error should mention unknown agent, got %q", err.Error())
	}
}

func TestFirewallApprovalApproveThenPasses(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-appr"))
	allowed, _, approval, err := f.Check("agent-appr", "security", "write")
	if err != nil {
		t.Fatalf("initial Check: %v", err)
	}
	if allowed || approval == nil {
		t.Fatalf("expected denied + pending approval, got allowed=%v approval=%v", allowed, approval)
	}
	if approval.Status != "pending" {
		t.Fatalf("approval status = %s, want pending", approval.Status)
	}

	if err := f.ApproveAction(approval.ID, "human-1"); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}

	allowed2, risk2, approval2, err := f.Check("agent-appr", "security", "write")
	if err != nil {
		t.Fatalf("re-check after approve: %v", err)
	}
	if !allowed2 {
		t.Error("re-check after approval should be allowed")
	}
	if approval2 != nil {
		t.Errorf("re-check should not create a new pending approval, got %+v", approval2)
	}
	if risk2.Level != domain.RiskHigh {
		t.Errorf("risk level = %s, want HIGH", risk2.Level)
	}
}

func TestFirewallApprovalRejectFails(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-rej"))
	_, _, approval, err := f.Check("agent-rej", "security", "write")
	if err != nil {
		t.Fatalf("initial Check: %v", err)
	}
	if approval == nil {
		t.Fatal("expected a pending approval")
	}
	if err := f.RejectAction(approval.ID, "human-1", "not now"); err != nil {
		t.Fatalf("RejectAction: %v", err)
	}

	allowed, _, approval2, err := f.Check("agent-rej", "security", "write")
	if err != nil {
		t.Fatalf("re-check after rejection should not error: %v", err)
	}
	if allowed {
		t.Error("re-check after rejection should still be denied")
	}
	if approval2 == nil || approval2.Status != "pending" {
		t.Errorf("re-check after rejection should issue a new pending approval, got %+v", approval2)
	}
}

func TestFirewallAuditLogRecordsDecisions(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-audit"))
	// allowed
	if _, _, _, err := f.Check("agent-audit", "tests", "write"); err != nil {
		t.Fatalf("tests:write: %v", err)
	}
	// pending
	if _, _, _, err := f.Check("agent-audit", "security", "write"); err != nil {
		t.Fatalf("security:write: %v", err)
	}
	// blocked
	if _, _, _, err := f.Check("agent-audit", "database", "drop"); err == nil {
		t.Fatal("database:drop should be blocked")
	}
	// denied (unknown agent)
	if _, _, _, err := f.Check("ghost", "tests", "write"); err == nil {
		t.Fatal("unknown agent should be denied")
	}

	log := f.AuditLog()
	all := log.All()
	if len(all) != 4 {
		t.Fatalf("audit log has %d entries, want 4", len(all))
	}

	results := map[string]int{}
	for _, e := range all {
		results[e.Result]++
	}
	if results["allowed"] != 1 {
		t.Errorf("allowed count = %d, want 1", results["allowed"])
	}
	if results["pending"] != 1 {
		t.Errorf("pending count = %d, want 1", results["pending"])
	}
	if results["blocked"] != 1 {
		t.Errorf("blocked count = %d, want 1", results["blocked"])
	}
	if results["denied"] != 1 {
		t.Errorf("denied count = %d, want 1", results["denied"])
	}

	if got := log.Filter("agent-audit"); len(got) != 3 {
		t.Errorf("Filter(agent-audit) = %d, want 3", len(got))
	}
}

func TestFirewallApprovalAuditRecordsApproved(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-aa"))
	_, _, approval, err := f.Check("agent-aa", "security", "write")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if err := f.ApproveAction(approval.ID, "human-1"); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	log := f.AuditLog()
	foundApproved := false
	for _, e := range log.All() {
		if e.Result == "approved" {
			foundApproved = true
		}
	}
	if !foundApproved {
		t.Error("audit log should contain an approved decision")
	}
}

func TestFirewallWithPoliciesOverride(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-custom")).
		WithPolicies([]domain.Policy{
			{ID: "x", Name: "custom_rule", Rule: "HIGH custom.act", Scope: "custom", Enabled: true},
		})
	// The broad agent has no "custom" permission, so it is denied on permission.
	allowed, _, _, err := f.Check("agent-custom", "custom", "act")
	if err == nil {
		t.Fatal("must be denied without the custom permission")
	}
	if allowed {
		t.Error("action without permission should be denied")
	}
}

func TestDefaultPoliciesMatchSpec(t *testing.T) {
	policies := DefaultPolicies()
	if len(policies) != 7 {
		t.Fatalf("DefaultPolicies() = %d, want 7", len(policies))
	}
	expect := map[string]domain.RiskLevel{
		"source":        domain.RiskMedium,
		"documentation": domain.RiskLow,
		"security":      domain.RiskHigh,
		"production":    domain.RiskCritical,
		"database":      domain.RiskCritical,
		"tests":         domain.RiskLow,
		"config":        domain.RiskMedium,
	}
	assessor := NewRiskAssessor(policies)
	for _, p := range policies {
		want, ok := expect[p.Scope]
		if !ok {
			t.Errorf("policy %s has unexpected scope %q", p.Name, p.Scope)
			continue
		}
		action := "write"
		if p.Name == "production_deploy" {
			action = "deploy"
		}
		if p.Name == "database_drop" {
			action = "drop"
		}
		got := assessor.AssessAction(p.Scope, action)
		if got.Level != want {
			t.Errorf("policy %s level = %s, want %s", p.Name, got.Level, want)
		}
	}
}
