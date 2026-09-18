package app

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/loop"
)

// TestRunDoContextCancelledFailsTask guards the kern_do cancellation fix: a
// cancelled context fails the task (terminal + auditable) instead of leaving
// the autonomous L2 loop running in the background after the caller believes
// it stopped. runLoop checks ctx right after Create.
func TestRunDoContextCancelledFailsTask(t *testing.T) {
	ts := matrixPlatform(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task, _, err := ts.RunDoContext(ctx, "cancelled run", loop.L0)
	if err == nil {
		t.Fatal("RunDoContext: expected cancellation error, got nil")
	}
	if task == nil {
		t.Fatal("RunDoContext: task is nil")
	}
	if task.State != domain.TaskFailed {
		t.Fatalf("task state = %s, want FAILED", task.State)
	}
}

// TestRunWorkflowDefaultContextCancelledFailsTask guards the kern_workflow
// cancellation fix: a cancelled context fails the task before the workflow
// engine starts.
func TestRunWorkflowDefaultContextCancelledFailsTask(t *testing.T) {
	ts := matrixPlatform(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task, err := ts.RunWorkflowDefaultContext(ctx, "cancelled workflow")
	if err == nil {
		t.Fatal("RunWorkflowDefaultContext: expected cancellation error, got nil")
	}
	if task == nil {
		t.Fatal("RunWorkflowDefaultContext: task is nil")
	}
	if task.State != domain.TaskFailed {
		t.Fatalf("task state = %s, want FAILED", task.State)
	}
}
