package rbac

import (
	"context"
	"strings"
	"testing"
)

func TestHandleRBAC(t *testing.T) {
	ctx := context.Background()

	// 1. Evaluate allowed tool for developer
	res, err := Handle(ctx, map[string]any{
		"action": "evaluate",
		"role":   "developer",
		"tool":   "kern_exec",
	})
	if err != nil {
		t.Fatalf("Handle evaluate failed: %v", err)
	}
	if !strings.Contains(res, "ALLOWED") {
		t.Errorf("expected ALLOWED for developer on kern_exec, got: %s", res)
	}

	// 2. Evaluate denied tool for junior_dev
	resDenied, err := Handle(ctx, map[string]any{
		"action": "evaluate",
		"role":   "junior_dev",
		"tool":   "kern_exec",
	})
	if err != nil {
		t.Fatalf("Handle evaluate junior_dev failed: %v", err)
	}
	if !strings.Contains(resDenied, "DENIED") {
		t.Errorf("expected DENIED for junior_dev on kern_exec, got: %s", resDenied)
	}

	// 3. Assign role
	resAssign, err := Handle(ctx, map[string]any{
		"action":   "assign",
		"agent_id": "bob",
		"role":     "admin",
	})
	if err != nil {
		t.Fatalf("Handle assign failed: %v", err)
	}
	if !strings.Contains(resAssign, "Assigned role \"admin\"") {
		t.Errorf("expected role assigned, got: %s", resAssign)
	}
}
