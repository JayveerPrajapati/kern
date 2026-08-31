package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestWorkflowApprovalThroughUI is the exit gate: a human can inspect
// a Task through the UI and approve/reject its approval gate. The UI's
// approve/reject/pending surfaces must read and write the SAME persistent
// approval store the agent-team workflow engine uses, so a workflow parked by
// kern_workflow/kern workflow (or any process) is resolvable by a human in the
// console.
func TestWorkflowApprovalThroughUI(t *testing.T) {
	root := fixtureRoot(t)

	// A workflow run parks at its human approval gate (the approval is
	// persisted to .kern/approvals.json by the engine).
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil).WithAgentID("test")
	task, err := ts.RunWorkflowDefault("helper")
	if err == nil {
		t.Fatal("workflow should park at the approval gate")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatalf("no approval ID: %v", err)
	}

	// The web console built over the SAME root.
	webApp, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}

	// Inspect the task through the UI (task detail page).
	rec := get(t, webApp, "/task/"+task.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /task/%s = %d, want 200 (inspect)", task.ID, rec.Code)
	}
	if !strings.Contains(rec.Body.String(), task.ID) {
		t.Error("task detail page does not render the task id")
	}

	// The pending approvals surfaced by the UI must include the workflow gate.
	rec = get(t, webApp, "/api/approvals/pending")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/approvals/pending = %d, want 200", rec.Code)
	}
	var pending []domainApproval
	if err := json.Unmarshal(rec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("parse pending: %v", err)
	}
	found := false
	for _, ap := range pending {
		if ap.ID == approvalID {
			found = true
		}
	}
	if !found {
		t.Fatalf("UI pending approvals missing workflow gate %s (got %d approvals)", approvalID, len(pending))
	}

	// A human approves through the UI.
	rec = postJSON(t, webApp, "/api/approvals/approve",
		`{"id":"`+approvalID+`","approver":"ui-operator"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/approvals/approve = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	// The workflow engine (fresh TaskService over the same root) sees the UI
	// decision and completes the run — the human's UI action unblocked it.
	resumed, err := webApp.taskSvc.RunWorkflowResume(task.ID)
	if err != nil {
		t.Fatalf("RunWorkflowResume after UI approval: %v", err)
	}
	if string(resumed.State) != "COMPLETED" {
		t.Fatalf("state = %s, want COMPLETED after UI approval", resumed.State)
	}
}

// TestWorkflowRejectionThroughUI verifies a human can REJECT a task's approval
// gate through the UI: the workflow must NOT proceed past the gate.
func TestWorkflowRejectionThroughUI(t *testing.T) {
	root := fixtureRoot(t)

	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil).WithAgentID("test")
	task, err := ts.RunWorkflowDefault("helper")
	if err == nil {
		t.Fatal("workflow should park at the approval gate")
	}
	approvalID := agent.ApprovalID(err)

	webApp, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	rec := postJSON(t, webApp, "/api/approvals/reject",
		`{"id":"`+approvalID+`","approver":"ui-operator"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/approvals/reject = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	resumed, err := webApp.taskSvc.RunWorkflowResume(task.ID)
	if err == nil {
		t.Fatal("rejected workflow must not complete")
	}
	if resumed == nil {
		t.Fatal("resume returned nil task")
	}
	// The rejected gate must still park the task (a rejected approval is not
	// satisfied; the engine requests a fresh one and parks again).
	if resumed.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %s, want WAITING_FOR_APPROVAL after rejection", resumed.State)
	}
}
