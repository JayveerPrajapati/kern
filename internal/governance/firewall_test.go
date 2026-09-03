package governance

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
)

// broadAgent has a permission on every resource the firewall tests exercise.
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

func TestCheckUnknownAgentFailsClosed(t *testing.T) {
	f := NewFirewall()
	allowed, risk, approval, err := f.Check("ghost", "tests", "write")
	if err == nil {
		t.Fatal("Check should error for an unknown agent")
	}
	if allowed {
		t.Error("unknown agent should be denied")
	}
	if approval != nil {
		t.Errorf("approval should be nil, got %+v", approval)
	}
	if risk.Level != domain.RiskCritical {
		t.Errorf("risk level = %s, want CRITICAL for unknown agent", risk.Level)
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error should mention unknown agent, got %q", err.Error())
	}
}

func TestCheckLacksPermissionFailsClosed(t *testing.T) {
	limited := NewAgent("limited", "Limited", "coder",
		[]Permission{{Resource: "source", Action: "write"}})
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

func TestCheckLowRiskAllowedNoApproval(t *testing.T) {
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

func TestCheckMediumRiskAllowedNoApproval(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-med"))
	allowed, risk, approval, err := f.Check("agent-med", "source", "write")
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

func TestCheckHighRiskRequiresApproval(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-sec"))
	allowed, risk, approval, err := f.Check("agent-sec", "security", "write")
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
		t.Fatal("a pending approval should be returned for HIGH risk")
	}
	if approval.Status != "pending" {
		t.Errorf("approval status = %s, want pending", approval.Status)
	}
}

func TestCheckCriticalDropAlwaysBlocked(t *testing.T) {
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

func TestApproveActionThenCheckPasses(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-appr"))
	allowed, _, approval, err := f.Check("agent-appr", "security", "write")
	if err != nil {
		t.Fatalf("initial Check: %v", err)
	}
	if allowed || approval == nil {
		t.Fatalf("expected denied + pending approval, got allowed=%v approval=%v", allowed, approval)
	}
	if err := f.ApproveAction(approval.ID, "human-1"); err != nil {
		t.Fatalf("ApproveAction: %v", err)
	}
	// Idempotency: after approval, a re-check passes without a new approval.
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

func TestRejectActionThenCheckDenied(t *testing.T) {
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
	// A fresh pending approval is issued since the previous one was rejected.
	if approval2 == nil || approval2.Status != "pending" {
		t.Errorf("re-check should issue a new pending approval, got %+v", approval2)
	}
}

func TestApproveActionUnknown(t *testing.T) {
	f := NewFirewall()
	if err := f.ApproveAction("does-not-exist", "human"); err == nil {
		t.Error("ApproveAction on unknown approval should error")
	}
}

func TestRejectActionUnknown(t *testing.T) {
	f := NewFirewall()
	if err := f.RejectAction("does-not-exist", "human", "no"); err == nil {
		t.Error("RejectAction on unknown approval should error")
	}
}

func TestWithPoliciesOverridesDefaults(t *testing.T) {
	// Override so that source.write is HIGH: it must now require approval.
	f := NewFirewall().WithAgents(broadAgent("agent-custom"))
	f.WithPolicies([]domain.Policy{
		{ID: "p1", Name: "high_src", Rule: "HIGH source.write", Scope: "source", Enabled: true},
	})
	allowed, risk, approval, err := f.Check("agent-custom", "source", "write")
	if err != nil {
		t.Fatalf("Check with custom policy: %v", err)
	}
	if allowed {
		t.Error("HIGH custom policy should deny without approval")
	}
	if risk.Level != domain.RiskHigh {
		t.Errorf("risk level = %s, want HIGH", risk.Level)
	}
	if approval == nil {
		t.Error("expected a pending approval for HIGH custom policy")
	}
}

func TestPoliciesReturnsCurrent(t *testing.T) {
	f := NewFirewall()
	before := len(f.Policies())
	if before == 0 {
		t.Fatal("default firewall should have policies")
	}
	f.WithPolicies([]domain.Policy{{ID: "x", Name: "only", Rule: "LOW x.y", Scope: "x", Enabled: true}})
	after := f.Policies()
	if len(after) != 1 {
		t.Fatalf("Policies() after override = %d, want 1", len(after))
	}
}

func TestTaskKeyFormat(t *testing.T) {
	got := TaskKey("agent-1", "source", "write")
	if got != "agent-1|source|write" {
		t.Errorf("TaskKey = %q, want agent-1|source|write", got)
	}
}

func TestWithAgentsSkipsNil(t *testing.T) {
	f := NewFirewall().WithAgents(nil, broadAgent("real"))
	if _, _, _, err := f.Check("real", "tests", "write"); err != nil {
		t.Fatalf("real agent should be registered: %v", err)
	}
	if _, _, _, err := f.Check("ghost", "tests", "write"); err == nil {
		t.Fatal("nil agent should not be registered")
	}
}

func TestWithBusPublishesLifecycle(t *testing.T) {
	bus := eventbus.New()
	var mu sync.Mutex
	kinds := map[eventbus.Kind]int{}
	bus.Subscribe("", func(ev eventbus.Event) {
		mu.Lock()
		defer mu.Unlock()
		kinds[ev.Kind]++
	})

	f := NewFirewall().WithBus(bus).WithAgents(broadAgent("agent-bus"))
	if allowed, _, _, err := f.Check("agent-bus", "source", "write"); err != nil || !allowed {
		t.Fatalf("Check(source.write) = %v, %v", allowed, err)
	}
	waitKinds(t, kinds, &mu, eventbus.PolicyEvaluated)
	if _, _, _, err := f.Check("agent-bus", "source", "drop"); err == nil {
		t.Fatal("expected error for denied action")
	}
	waitKinds(t, kinds, &mu, eventbus.PolicyBlocked)
}

func waitKinds(t *testing.T, kinds map[eventbus.Kind]int, mu *sync.Mutex, want ...eventbus.Kind) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		all := true
		for _, k := range want {
			if kinds[k] == 0 {
				all = false
				break
			}
		}
		mu.Unlock()
		if all {
			return
		}
		time.Sleep(time.Millisecond)
	}
	for _, k := range want {
		mu.Lock()
		n := kinds[k]
		mu.Unlock()
		if n == 0 {
			t.Errorf("no %s published", k)
		}
	}
}

func TestNilBusIsNoOp(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-nobus"))
	if allowed, _, _, err := f.Check("agent-nobus", "source", "write"); err != nil || !allowed {
		t.Fatalf("allowed = %v, err = %v", allowed, err)
	}
}

func TestAuditLogAccessor(t *testing.T) {
	f := NewFirewall().WithAgents(broadAgent("agent-aud"))
	f.Check("agent-aud", "source", "write")
	if got := f.AuditLog().All(); len(got) == 0 {
		t.Error("AuditLog should contain the recorded decision")
	}
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

func TestFirewallConcurrentCheckAndApprove(t *testing.T) {
	agent := broadAgent("concurrent-agent")
	f := NewFirewall().WithAgents(agent)
	f.WithPolicies([]domain.Policy{
		{ID: "p1", Name: "high_risk", Rule: "HIGH source.write", Scope: "source", Enabled: true},
	})

	const numGoroutines = 20
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// Half the goroutines call Check
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _, _, _ = f.Check("concurrent-agent", "source", "write")
			}
		}(i)
	}

	// The other half concurrently Approve and Reject actions
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				taskKey := "concurrent-agent|source|write"
				f.mu.Lock()
				f.approvedKeys[taskKey] = true
				f.mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
}
