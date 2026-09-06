package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleAgentCoordination(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// 1. Claim resource
	resClaim, err := srv.handleAgentCoordination(ctx, map[string]any{
		"action":      "claim",
		"agent_id":    "agent-alice",
		"resource":    "auth-service",
		"ttl_seconds": 60,
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
	})
	if err != nil {
		t.Fatalf("handleAgentCoordination release failed: %v", err)
	}
	if !strings.Contains(resRelease, "released successfully") {
		t.Errorf("expected released successfully in report, got: %s", resRelease)
	}
}
