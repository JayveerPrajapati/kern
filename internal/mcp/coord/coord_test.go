package coord

import (
	"context"
	"strings"
	"testing"
)

func TestHandleCoordination(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// 1. Claim resource
	resClaim, err := Handle(ctx, map[string]any{
		"action":      "claim",
		"agent_id":    "agent-alice",
		"resource":    "auth-service",
		"ttl_seconds": 60,
		"root":        root,
	})
	if err != nil {
		t.Fatalf("Handle claim failed: %v", err)
	}
	if !strings.Contains(resClaim, "successfully claimed") {
		t.Errorf("expected successfully claimed in report, got: %s", resClaim)
	}

	// 2. Conflicting claim
	resConflict, err := Handle(ctx, map[string]any{
		"action":   "claim",
		"agent_id": "agent-bob",
		"resource": "auth-service",
		"root":     root,
	})
	if err != nil {
		t.Fatalf("Handle conflict check failed: %v", err)
	}
	if !strings.Contains(resConflict, "already claimed") {
		t.Errorf("expected already claimed in conflict report, got: %s", resConflict)
	}

	// 3. Handoff
	resHandoff, err := Handle(ctx, map[string]any{
		"action":     "handoff",
		"from_agent": "agent-alice",
		"to_agent":   "agent-bob",
		"task_id":    "refactor-auth",
		"notes":      "completed interface, tests needed",
		"root":       root,
	})
	if err != nil {
		t.Fatalf("Handle handoff failed: %v", err)
	}
	if !strings.Contains(resHandoff, "registered") {
		t.Errorf("expected registered in handoff report, got: %s", resHandoff)
	}

	// 4. Release resource
	resRelease, err := Handle(ctx, map[string]any{
		"action":   "release",
		"agent_id": "agent-alice",
		"resource": "auth-service",
		"root":     root,
	})
	if err != nil {
		t.Fatalf("Handle release failed: %v", err)
	}
	if !strings.Contains(resRelease, "released successfully") {
		t.Errorf("expected released successfully, got: %s", resRelease)
	}
}
