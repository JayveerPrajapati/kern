package app

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/governance/identity"
)

// TestPolicyPrecheckPass verifies the unified policy precheck (Phase 6.4) allows
// a request that clears identity, scope, permission, environment, and risk: a
// known agent with the exact permission, a resource inside the task scope, and
// an allowed environment.
func TestPolicyPrecheckPass(t *testing.T) {
	fw := governance.NewFirewall()
	fw.WithAgents(identity.NewAgent("agent-1", "docs", "cli",
		[]identity.Permission{{Resource: "documentation", Action: "write"}}))
	ts := NewTaskService(&Platform{root: t.TempDir(), fw: fw}, nil)

	res := ts.PolicyPrecheck(context.Background(), domain.PrecheckRequest{
		AgentID:     "agent-1",
		TaskID:      "task-1",
		Resource:    "documentation",
		Action:      "write",
		Environment: "development",
		Scope: domain.TaskScope{
			TaskID: "task-1",
			Paths:  []string{"documentation"},
			Envs:   []string{"development"},
		},
	})

	if !res.Allowed {
		t.Fatalf("Allowed=false, want true; denied=%v deny=%+v", res.Denied, res.DenyReason)
	}
	if res.Denied {
		t.Error("Denied=true, want false")
	}
	if res.DenyReason != nil {
		t.Errorf("DenyReason=%+v, want nil on pass", res.DenyReason)
	}
}

// TestPolicyPrecheckDenyPermission verifies a permission-denied request is
// denied with a firewall-stage deny reason (identity/permission gate).
func TestPolicyPrecheckDenyPermission(t *testing.T) {
	fw := governance.NewFirewall()
	// The agent has no permission for the requested resource/action -> firewall
	// denies (fail-closed).
	fw.WithAgents(identity.NewAgent("agent-1", "docs", "cli", nil))
	ts := NewTaskService(&Platform{root: t.TempDir(), fw: fw}, nil)

	res := ts.PolicyPrecheck(context.Background(), domain.PrecheckRequest{
		AgentID:     "agent-1",
		TaskID:      "task-1",
		Resource:    "documentation",
		Action:      "write",
		Environment: "development",
		Scope: domain.TaskScope{
			TaskID: "task-1",
			Paths:  []string{"documentation"},
			Envs:   []string{"development"},
		},
	})

	if !res.Denied {
		t.Fatal("Denied=false, want true (permission denied)")
	}
	if res.Allowed {
		t.Error("Allowed=true, want false on deny")
	}
	if res.DenyReason == nil {
		t.Fatal("DenyReason is nil, want a firewall deny reason")
	}
	if res.DenyReason.Stage != "firewall" {
		t.Errorf("DenyReason.Stage=%q, want firewall", res.DenyReason.Stage)
	}
	if res.Risk.Level == "" {
		t.Error("Risk.Level is empty; want a non-zero preliminary risk")
	}
}

// TestPolicyPrecheckDenyEnv verifies an out-of-scope environment is denied at
// the env gate, even when identity and permission would otherwise pass.
func TestPolicyPrecheckDenyEnv(t *testing.T) {
	fw := governance.NewFirewall()
	fw.WithAgents(identity.NewAgent("agent-1", "docs", "cli",
		[]identity.Permission{{Resource: "documentation", Action: "write"}}))
	ts := NewTaskService(&Platform{root: t.TempDir(), fw: fw}, nil)

	res := ts.PolicyPrecheck(context.Background(), domain.PrecheckRequest{
		AgentID:     "agent-1",
		TaskID:      "task-1",
		Resource:    "documentation",
		Action:      "write",
		Environment: "production", // outside the task's allowed envs
		Scope: domain.TaskScope{
			TaskID: "task-1",
			Paths:  []string{"docs/"},
			Envs:   []string{"development"},
		},
	})

	if !res.Denied {
		t.Fatal("Denied=false, want true (environment outside scope)")
	}
	if res.DenyReason == nil || res.DenyReason.Stage != "env" {
		t.Errorf("DenyReason=%+v, want env stage", res.DenyReason)
	}
}

// TestRunSurfacesPrecheck verifies the unified precheck is wired into
// TaskService.Run so a precheck result is available before execution.
func TestRunSurfacesPrecheck(t *testing.T) {
	svc, _ := newTestTaskService(t)
	result, err := svc.Run("add caching to UserService")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Precheck == nil {
		t.Fatal("RunResult.Precheck is nil, want the unified precheck result")
	}
	if !result.Precheck.Allowed {
		t.Errorf("Precheck.Allowed=false, want true; deny=%v", result.Precheck.DenyReason)
	}
}
