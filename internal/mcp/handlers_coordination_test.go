package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/coord"
)

func TestHandleAgentCoordination(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	root := t.TempDir()

	// 1. Claim resource
	resClaim, err := srv.handleAgentCoordination(ctx, map[string]any{
		"action":      "claim",
		"agent_id":    "agent-alice",
		"resource":    "auth-service",
		"ttl_seconds": 60,
		"root":        root,
	})
	if err != nil {
		t.Fatalf("handleAgentCoordination claim failed: %v", err)
	}
	if !strings.Contains(resClaim, "successfully claimed") {
		t.Errorf("expected successfully claimed in report, got: %s", resClaim)
	}

	// 2. Conflicting claim
	resConflict, err := srv.handleAgentCoordination(ctx, map[string]any{
		"action":   "claim",
		"agent_id": "agent-bob",
		"resource": "auth-service",
		"root":     root,
	})
	if err != nil {
		t.Fatalf("handleAgentCoordination conflict check failed: %v", err)
	}
	if !strings.Contains(resConflict, "already claimed") {
		t.Errorf("expected already claimed in conflict report, got: %s", resConflict)
	}

	// 3. Handoff
	resHandoff, err := srv.handleAgentCoordination(ctx, map[string]any{
		"action":     "handoff",
		"from_agent": "agent-alice",
		"to_agent":   "agent-bob",
		"task_id":    "refactor-auth",
		"notes":      "completed interface, tests needed",
		"root":       root,
	})
	if err != nil {
		t.Fatalf("handleAgentCoordination handoff failed: %v", err)
	}
	if !strings.Contains(resHandoff, "registered") {
		t.Errorf("expected registered in handoff report, got: %s", resHandoff)
	}

	// 4. Release resource
	resRelease, err := srv.handleAgentCoordination(ctx, map[string]any{
		"action":   "release",
		"agent_id": "agent-alice",
		"resource": "auth-service",
		"root":     root,
	})
	if err != nil {
		t.Fatalf("handleAgentCoordination release failed: %v", err)
	}
	if !strings.Contains(resRelease, "released successfully") {
		t.Errorf("expected released successfully in report, got: %s", resRelease)
	}
}

// TestAgentCoordinationClaimsPersistAcrossInstances proves that a claim made
// through one handler instance round-trips through the on-disk state
// (<root>/.kern/coordination/claims.json) into a second, fresh instance — the
// equivalent of a fresh process — and that a release is equally durable.
func TestAgentCoordinationClaimsPersistAcrossInstances(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// Instance 1 claims a resource.
	srvA := newTestServer()
	if _, err := srvA.handleAgentCoordination(ctx, map[string]any{
		"action":      "claim",
		"agent_id":    "agent-alice",
		"resource":    "auth-service",
		"ttl_seconds": 60,
		"root":        root,
	}); err != nil {
		t.Fatalf("claim failed: %v", err)
	}

	// Simulate a fresh process: drop all in-memory package state. Only the
	// persisted claims.json may survive.
	coord.ResetMemory()

	// Instance 2 (fresh state): status must see the persisted claim.
	srvB := newTestServer()
	status, err := srvB.handleAgentCoordination(ctx, map[string]any{
		"action": "status",
		"root":   root,
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(status, "**Active Claims:** 1") {
		t.Fatalf("expected 1 active claim after restart, got: %s", status)
	}

	// Release from instance 2.
	if _, err := srvB.handleAgentCoordination(ctx, map[string]any{
		"action":   "release",
		"agent_id": "agent-alice",
		"resource": "auth-service",
		"root":     root,
	}); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Another fresh process: status must show the release (0 claims).
	coord.ResetMemory()

	srvC := newTestServer()
	status, err = srvC.handleAgentCoordination(ctx, map[string]any{
		"action": "status",
		"root":   root,
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(status, "**Active Claims:** 0") {
		t.Fatalf("expected 0 active claims after release, got: %s", status)
	}
}

// TestAgentCoordinationHandoffsPersistAcrossInstances proves status counts
// on-disk handoff records (hf-*.json) made by an earlier instance.
func TestAgentCoordinationHandoffsPersistAcrossInstances(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	srvA := newTestServer()
	if _, err := srvA.handleAgentCoordination(ctx, map[string]any{
		"action":     "handoff",
		"from_agent": "agent-alice",
		"to_agent":   "agent-bob",
		"task_id":    "refactor-auth",
		"notes":      "tests needed",
		"root":       root,
	}); err != nil {
		t.Fatalf("handoff failed: %v", err)
	}

	// Simulate a fresh process: only the hf-*.json on disk remains.
	coord.ResetMemory()

	srvB := newTestServer()
	status, err := srvB.handleAgentCoordination(ctx, map[string]any{
		"action": "status",
		"root":   root,
	})
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(status, "**Total Handoffs:** 1") {
		t.Fatalf("expected 1 handoff counted from disk after restart, got: %s", status)
	}
}
