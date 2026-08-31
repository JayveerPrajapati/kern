package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestPolicyPrecheckPass verifies the unified policy precheck allows
// a request that clears identity, scope, permission, environment, and risk: a
// known agent with the exact permission, a resource inside the task scope, and
// an allowed environment.
func TestPolicyPrecheckPass(t *testing.T) {
	fw := governance.NewFirewall()
	fw.WithAgents(governance.NewAgent("agent-1", "docs", "cli",
		[]governance.Permission{{Resource: "documentation", Action: "write"}}))
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
	fw.WithAgents(governance.NewAgent("agent-1", "docs", "cli", nil))
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
	fw.WithAgents(governance.NewAgent("agent-1", "docs", "cli",
		[]governance.Permission{{Resource: "documentation", Action: "write"}}))
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

// TestRunNextActionReflectsDeniedPrecheck verifies that a denied precheck is
// authoritative on the RunResult: when the firewall denies the intent's
// resource/action, TaskService.Run must surface a blocked next action instead
// of "execute workflow". Otherwise an operator following the plan would be told
// to execute a change its own policy precheck already denied.
func TestRunNextActionReflectsDeniedPrecheck(t *testing.T) {
	fw := governance.NewFirewall()
	// agent-1 has no permission for CODE_CHANGE's representative action
	// ("write" on the "repository" resource) -> the firewall denies fail-closed.
	fw.WithAgents(governance.NewAgent("agent-1", "docs", "cli", nil))
	svc := NewTaskService(&Platform{root: t.TempDir(), fw: fw}, nil)
	svc.WithAgentID("agent-1")

	result, err := svc.Run("add caching to UserService")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Precheck == nil || !result.Precheck.Denied {
		t.Fatalf("Precheck.Denied=false, want true (agent-1 has no write permission); precheck=%+v", result.Precheck)
	}
	if result.ApprovalState != "denied" {
		t.Errorf("ApprovalState=%q, want %q", result.ApprovalState, "denied")
	}
	if !strings.Contains(result.NextAction, "precheck denied") {
		t.Errorf("NextAction=%q, want it to report the precheck denial", result.NextAction)
	}
	if strings.Contains(result.NextAction, "execute workflow") {
		t.Errorf("NextAction=%q must not claim execute workflow when precheck denies", result.NextAction)
	}
}

// TestKernRunOperatorClearsPrecheck locks the exit gate: the default
// control-plane operator identity ("kern" — TaskService's default agent, used
// by `kern run` and the kern_run MCP tool) is registered on the real Platform
// firewall with the permissions the intent compiler produces, so every intent
// type clears the identity/permission gate and kern_run returns
// "execute workflow" instead of a policy denial. An unregistered agent must
// still fail closed (covered by TestRunNextActionReflectsDeniedPrecheck).
func TestKernRunOperatorClearsPrecheck(t *testing.T) {
	p, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Default agent ID on TaskService is "kern" (task.go:83).
	ts := NewTaskService(p, nil)

	intents := []string{
		"Add tenant-aware caching to UserService", // CODE_CHANGE → repository:write
		"Explain how caching works",               // UNDERSTAND → repository:read
		"Review the auth module",                  // REVIEW → repository:read
		"What if we removed tenant_cache",         // WHAT_IF → repository:read
		"Investigate checkout 500s incident",      // INCIDENT → repository:read
		"Security scan the login flow",            // SECURITY → repository:scan
		"Test the cache package",                  // TEST → repository:read
		"Audit who changed task.go",               // AUDIT → repository:audit
		"Modernize the monolith",                  // MODERNIZATION → repository:write
	}
	for _, intent := range intents {
		res, err := ts.Run(intent)
		if err != nil {
			t.Fatalf("Run(%q): %v", intent, err)
		}
		if res.Precheck == nil {
			t.Fatalf("Run(%q): Precheck is nil", intent)
		}
		if !res.Precheck.Allowed {
			t.Errorf("Run(%q): precheck denied for default kern operator: %+v", intent, res.Precheck.DenyReason)
		}
		if res.ApprovalState == "denied" {
			t.Errorf("Run(%q): approval state = denied", intent)
		}
	}
}
