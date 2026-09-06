package mcp

import (
	"context"
	"strings"
	"testing"
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
