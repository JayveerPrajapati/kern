package mcp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/rbac"
)

func TestHandleAgentRoleRBAC(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// 1. Junior dev denied kern_exec
	resEval, err := srv.handleAgentRoleRBAC(ctx, map[string]any{
		"agent_id": "junior-1",
		"role":     "junior_dev",
		"tool":     "kern_exec",
	})
	if err != nil {
		t.Fatalf("handleAgentRoleRBAC failed: %v", err)
	}
	if !strings.Contains(resEval, "DENIED") {
		t.Errorf("expected DENIED for junior_dev on kern_exec, got: %s", resEval)
	}

	// 2. Junior dev allowed kern_compact_file
	resEval2, err := srv.handleAgentRoleRBAC(ctx, map[string]any{
		"agent_id": "junior-1",
		"role":     "junior_dev",
		"tool":     "kern_compact_file",
	})
	if err != nil {
		t.Fatalf("handleAgentRoleRBAC failed: %v", err)
	}
	if !strings.Contains(resEval2, "ALLOWED") {
		t.Errorf("expected ALLOWED for junior_dev on kern_compact_file, got: %s", resEval2)
	}
}

// assignRole is a test helper that assigns a role through the real RBAC
// handler with the operator opt-in env var set, then restores state.
func assignRole(t *testing.T, agentID, role string) {
	t.Helper()
	if err := os.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	ctx := context.Background()
	if _, err := rbac.Handle(ctx, map[string]any{
		"action":   "assign",
		"agent_id": agentID,
		"role":     role,
	}); err != nil {
		t.Fatalf("assign role %q to %q: %v", role, agentID, err)
	}
}

// TestRBACEnforcementAtDispatch pins the dispatch-path enforcement: an agent
// with an assigned role may only invoke tools its role grants, and the denial
// surfaces as a clear tool error before any handler side effect runs.
func TestRBACEnforcementAtDispatch(t *testing.T) {
	rbac.ResetMemory()
	defer rbac.ResetMemory()
	defer os.Unsetenv("KERN_ALLOW_RBAC_ASSIGN")

	srv := newTestServer()
	ctx := context.Background()

	assignRole(t, "junior-1", "junior_dev")
	assignRole(t, "boss", "admin")

	// 1. Assigned restricted role: kern_exec is denied (junior_dev).
	_, err := srv.runTool(ctx, "t1", "", "kern_exec", map[string]any{"agent_id": "junior-1"})
	if err == nil || !strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected junior_dev to be RBAC-denied on kern_exec, got: %v", err)
	}

	// 2. Same agent: an allowed tool passes the RBAC gate (handler errors on
	// missing args are fine — the gate must not deny it).
	_, err = srv.runTool(ctx, "t2", "", "kern_compact_file", map[string]any{"agent_id": "junior-1"})
	if err != nil && strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected junior_dev to pass RBAC on kern_compact_file, got: %v", err)
	}

	// 3. Meta routing is gated too: kern_meta is not in junior_dev's
	// AllowedTools, so the request never reaches the exec sub-tool.
	_, err = srv.runTool(ctx, "t3", "", "kern_meta", map[string]any{
		"agent_id": "junior-1",
		"request":  "run the exec tool",
	})
	if err == nil || !strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected junior_dev to be RBAC-denied on kern_meta routing to exec, got: %v", err)
	}

	// 4. admin ("*" AllowedTools) is not blocked by the RBAC gate.
	_, err = srv.runTool(ctx, "t4", "", "kern_exec", map[string]any{"agent_id": "boss"})
	if err != nil && strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected admin to pass RBAC on kern_exec, got: %v", err)
	}

	// 5. An agent with no assigned role keeps the legacy trust model.
	_, err = srv.runTool(ctx, "t5", "", "kern_exec", map[string]any{"agent_id": "no-role-xyz"})
	if err != nil && strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected unassigned agent to pass RBAC, got: %v", err)
	}

	// 6. No agent_id at all (default agent) still works unchanged.
	_, err = srv.runTool(ctx, "t6", "", "kern_exec", map[string]any{})
	if err != nil && strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected default agent to pass RBAC, got: %v", err)
	}

	// 7. CallTool (public API) goes through the same gate.
	_, err = srv.CallTool(ctx, "kern_exec", map[string]any{"agent_id": "junior-1"})
	if err == nil || !strings.Contains(err.Error(), "RBAC denied") {
		t.Errorf("expected CallTool to RBAC-deny junior_dev on kern_exec, got: %v", err)
	}
}

// TestRBACAssignNotSelfService pins the fail-closed gate on the assign action:
// without KERN_ALLOW_RBAC_ASSIGN=1 a client cannot assign a role (e.g. admin)
// to itself through the dispatch path.
func TestRBACAssignNotSelfService(t *testing.T) {
	rbac.ResetMemory()
	defer rbac.ResetMemory()
	defer os.Unsetenv("KERN_ALLOW_RBAC_ASSIGN")

	srv := newTestServer()
	ctx := context.Background()

	// No env var set: assign fails closed.
	_, err := srv.handleAgentRoleRBAC(ctx, map[string]any{
		"action":   "assign",
		"agent_id": "myself",
		"role":     "admin",
	})
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Errorf("expected assign to fail closed without KERN_ALLOW_RBAC_ASSIGN, got: %v", err)
	}

	// The privilege escalation must not have happened.
	if allowed, _ := rbac.CheckAgentTool("myself", "kern_exec"); !allowed {
		t.Errorf("assign must not have taken effect; expected myself to stay unassigned")
	}

	// With the env var set, assignment works (operator opt-in).
	if err := os.Setenv("KERN_ALLOW_RBAC_ASSIGN", "1"); err != nil {
		t.Fatalf("setenv: %v", err)
	}
	res, err := srv.handleAgentRoleRBAC(ctx, map[string]any{
		"action":   "assign",
		"agent_id": "myself",
		"role":     "admin",
	})
	if err != nil {
		t.Fatalf("assign with env opt-in failed: %v", err)
	}
	if !strings.Contains(res, "Assigned role \"admin\"") {
		t.Errorf("expected assignment success, got: %s", res)
	}
}
