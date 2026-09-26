package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestHandleDeployRequiresTaskID: kern_deploy without a task id is rejected.
func TestHandleDeployRequiresTaskID(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()
	if _, err := srv.handleDeploy(ctx, map[string]any{}); err == nil {
		t.Fatal("handleDeploy() = nil error without task_id, want error")
	} else if !strings.Contains(err.Error(), "task_id") {
		t.Errorf("error = %q, want it to name task_id", err)
	}
}

// TestHandleDeployUnknownTask: deploying a nonexistent task surfaces the
// not-found error (same text the CLI and web surface).
func TestHandleDeployUnknownTask(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()
	_, err := srv.handleDeploy(ctx, map[string]any{"task_id": "task-nope"})
	if err == nil {
		t.Fatal("handleDeploy() = nil error for unknown task, want error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to mention not found", err)
	}
}
