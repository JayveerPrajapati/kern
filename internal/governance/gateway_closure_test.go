package governance

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance/firewall"
	"github.com/JayveerPrajapati/kern/internal/governance/identity"
)

// TestEvaluateScopedFullServiceArtifactGates verifies that EvaluateScopedFull
// enforces the service and artifact scope dimensions (P7.3) in addition to the
// env/path gates, and that denials carry a non-empty Policy and
// SafeAlternative (P7.6).
func TestEvaluateScopedFullServiceArtifactGates(t *testing.T) {
	fw := firewall.NewFirewall()
	fw.WithAgents(identity.NewAgent("agent-1", "a", "coder", []identity.Permission{
		{Resource: "payments/ref.go", Action: "write"},
		{Resource: "payments/refund.go", Action: "write"},
	}))
	gw := NewToolGateway(fw)

	scope := domain.TaskScope{
		TaskID:    "t1",
		Paths:     []string{"payments/"},
		Services:  []string{"payments"},
		Envs:      []string{"dev"},
		Artifacts: []string{"code_patch"},
	}

	// Service not in scope -> DENY at the service gate.
	res := gw.EvaluateScopedFull("agent-1", "t1", "payments/refund.go", "write", "billing", "code_patch", "dev", scope, nil)
	if res.Decision != domain.DecisionDenied {
		t.Fatalf("service gate: decision = %q, want DENY", res.Decision)
	}
	if res.Deny == nil || res.Deny.Stage != "service" {
		t.Fatalf("service gate: deny = %+v, want stage 'service'", res.Deny)
	}
	if res.Deny.Policy == "" || res.Deny.SafeAlternative == "" {
		t.Errorf("service gate: deny.Policy=%q deny.SafeAlternative=%q, want both non-empty", res.Deny.Policy, res.Deny.SafeAlternative)
	}

	// Artifact kind not in scope -> ARTIFACT at the artifact stage.
	res = gw.EvaluateScopedFull("agent-1", "t1", "payments/ref.go", "write", "payments", "binary", "dev", scope, nil)
	if res.Decision != domain.DecisionDenied {
		t.Fatalf("artifact gate: decision = %v, want DENY", res.Decision)
	}
	if res.Deny == nil || res.Deny.Stage != "artifact" {
		t.Fatalf("artifact gate: deny = %+v, want stage 'artifact'", res.Deny)
	}
	if res.Deny.Policy == "" || res.Deny.SafeAlternative == "" {
		t.Errorf("artifact gate: deny.Policy=%q deny.SafeAlternative=%q, want both non-empty", res.Deny.Policy, res.Deny.SafeAlternative)
	}

	// Matching service + artifact + env + path -> ALLOWED.
	res = gw.EvaluateScopedFull("agent-1", "t1", "payments/ref.go", "write", "payments", "code_patch", "dev", scope, nil)
	if res.Decision != domain.DecisionAllowed || !res.Allowed {
		t.Errorf("matching scope: res = %+v, want ALLOW", res)
	}
}

// TestDenyReasonPolicyAndSafeAlternative verifies that every denial stage
// reachable through EvaluateScoped carries a non-empty Policy and
// SafeAlternative (P7.6 explain-deny).
func TestDenyReasonPolicyAndSafeAlternative(t *testing.T) {
	// env denial.
	fw := firewall.NewFirewall()
	gw := NewToolGateway(fw)
	res := gw.EvaluateScoped("agent-1", "t1", "file.go", "read", "production", domain.TaskScope{TaskID: "t1", Envs: []string{"dev"}}, nil)
	if res.Deny == nil || res.Deny.Policy == "" || res.Deny.SafeAlternative == "" {
		t.Errorf("env stage: deny = %+v, want non-empty Policy and SafeAlternative", res.Deny)
	}

	// path denial.
	res = gw.EvaluateScoped("agent-1", "t1", "bad/x.go", "read", "", domain.TaskScope{TaskID: "t1", Paths: []string{"ok/"}}, nil)
	if res.Deny == nil || res.Deny.Policy == "" || res.Deny.SafeAlternative == "" {
		t.Errorf("boundary stage: deny = %+v, want non-empty Policy and SafeAlternative", res.Deny)
	}

	// firewall denial (unknown agent).
	res = gw.EvaluateScoped("ghost-agent", "t1", "file.go", "read", "", domain.TaskScope{TaskID: "t1"}, nil)
	if res.Deny == nil || res.Deny.Policy == "" || res.Deny.SafeAlternative == "" {
		t.Errorf("firewall stage: deny = %+v, want non-empty Policy and SafeAlternative", res.Deny)
	}

	// budget denial.
	fw2 := firewall.NewFirewall()
	fw2.WithAgents(identity.NewAgent("agent-1", "a", "coder", []identity.Permission{{Resource: "file.go", Action: "read"}}))
	gw2 := NewToolGateway(fw2)
	budget := &domain.SafetyBudget{MaxToolCalls: 1}
	budget.Start()
	budget.TrackToolCall()
	budget.TrackToolCall() // exceed
	res = gw2.EvaluateScoped("agent-1", "t1", "file.go", "read", "", domain.TaskScope{TaskID: "t1"}, budget)
	if res.Deny == nil || res.Deny.Policy == "" || res.Deny.SafeAlternative == "" {
		t.Errorf("budget stage: deny = %+v, want non-empty Policy and SafeAlternative", res.Deny)
	}
}

// TestEvaluateScopedWrapperBackwardCompat verifies the original EvaluateScoped
// (no service/artifact args) still returns ALLOW for a scope with empty
// Services/Artifacts and a valid path+env+firewall, proving backward
// compatibility.
func TestEvaluateScopedWrapperBackwardCompat(t *testing.T) {
	fw := firewall.NewFirewall()
	fw.WithAgents(identity.NewAgent("agent-1", "a", "coder", []identity.Permission{{Resource: "ok/file.go", Action: "write"}}))
	gw := NewToolGateway(fw)

	scope := domain.TaskScope{
		TaskID:    "t1",
		Paths:     []string{"ok/"},
		Envs:      []string{"dev"},
		Services:  nil, // empty -> all services allowed
		Artifacts: nil, // empty -> all artifacts allowed
	}
	res := gw.EvaluateScoped("agent-1", "t1", "ok/file.go", "write", "dev", scope, nil)
	if res.Decision != domain.DecisionAllowed || !res.Allowed {
		t.Errorf("backward-compat: res = %+v, want ALLOW", res)
	}
	if res.Deny != nil {
		t.Errorf("backward-compat: deny should be nil, got %+v", res.Deny)
	}
}
