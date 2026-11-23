package tasklife

import (
	"context"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

func TestRunWorkflowResumeContextCancelledFailsTask(t *testing.T) {
	root := workflowFixtureRoot(t)
	p := newTestPlatform(t, root)
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, err := ts.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require approval")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatal("no approval ID surfaced")
	}
	if _, err := ts.ResolveApprovalForTask(approvalID, "human-1", true, "approved"); err != nil {
		t.Fatalf("ResolveApprovalForTask: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := ts.RunWorkflowResumeContext(ctx, task.ID)
	if err == nil {
		t.Fatal("RunWorkflowResumeContext: expected cancellation error")
	}
	if got == nil || got.State != domain.TaskFailed {
		t.Fatalf("task = %+v, want state FAILED", got)
	}
	if contains(stepActions(got), "code") {
		t.Fatalf("cancelled resume must not execute a code step: %v", stepActions(got))
	}
}

func TestRunWorkflowResumeContextCancelledCrossProcess(t *testing.T) {
	root := workflowFixtureRoot(t)
	p1 := newTestPlatform(t, root)
	ts1 := NewTaskService(p1, nil).WithAgentID("test")

	task, err := ts1.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require approval")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatal("no approval ID surfaced")
	}
	if _, err := governance.NewFileStore(root).Decide(approvalID, "human-1", true, "approved"); err != nil {
		t.Fatalf("out-of-band approval: %v", err)
	}

	p2 := newTestPlatform(t, root)
	ts2 := NewTaskService(p2, nil).WithAgentID("test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := ts2.RunWorkflowResumeContext(ctx, task.ID)
	if err == nil {
		t.Fatal("cross-process resume: expected cancellation error")
	}
	if got == nil || got.State != domain.TaskFailed {
		t.Fatalf("task = %+v, want state FAILED", got)
	}
	if contains(stepActions(got), "code") {
		t.Fatalf("cancelled cross-process resume must not execute a code step: %v", stepActions(got))
	}
}
