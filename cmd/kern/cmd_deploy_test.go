package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// deployableTaskFixture creates a task walked to PR_CREATED — the only state
// from which Deploy may proceed — and persisted to the task store, so the
// command's own TaskService (fresh registry) sees it. The default deployer is
// NoopDeployer (no KERN_DEPLOY_COMMAND), which skips the approval gate.
func deployableTaskFixture(t *testing.T) (root, taskID string) {
	t.Helper()
	root = newRoot(t)
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil).WithAgentID("test")
	task, err := ts.Create("deploy the release")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, st := range []domain.TaskState{
		domain.TaskAnalyzing, domain.TaskPlanning, domain.TaskWaitingApproval,
		domain.TaskApproved, domain.TaskExecuting, domain.TaskVerifying,
		domain.TaskReadyForPR, domain.TaskPRCreated,
	} {
		if err := task.Transition(st); err != nil {
			t.Fatalf("transition %s: %v", st, err)
		}
	}
	if _, err := agent.NewTaskStore(root).Save(*task); err != nil {
		t.Fatalf("persist: %v", err)
	}
	return root, task.ID
}

// TestDeploySuccess covers the happy path: a PR_CREATED task deploys
// (NoopDeployer) and the persisted task lands in DEPLOYING.
func TestDeploySuccess(t *testing.T) {
	root, id := deployableTaskFixture(t)
	out := captureStdout(t, func() { runDeploy([]string{"--root", root, id, "--version", "v1.2.3"}) })
	if !strings.Contains(out, "deployed: "+id) || !strings.Contains(out, "state DEPLOYING") {
		t.Fatalf("expected deploy success output, got %q", out)
	}
	got, err := agent.NewTaskStore(root).Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != domain.TaskDeploying {
		t.Errorf("task state = %s, want DEPLOYING", got.State)
	}
}

// TestDeployNotFound locks the unknown-task error path (exit 1, no silent no-op).
func TestDeployNotFound(t *testing.T) {
	root := newRoot(t)
	expectExit(t, 1, func() { runDeploy([]string{"--root", root, "t-missing"}) })
}

// TestDeployInvalidState locks the state gate: a task still in CREATED is not
// deployable and the command exits 1 with the deployable-state message.
func TestDeployInvalidState(t *testing.T) {
	root := newRoot(t)
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil)
	task, err := ts.Create("deploy the release")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	expectExit(t, 1, func() { runDeploy([]string{"--root", root, task.ID}) })
}
