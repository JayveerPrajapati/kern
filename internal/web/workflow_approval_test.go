package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
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
// gate through the UI: the workflow must NOT proceed past the gate. Since the
// approve-surface symmetry fix (F-026) routes the UI decision through the
// app-layer TaskService, the gated task is marked REJECTED immediately
// (mirroring `kern approve --reject`) and a resume cannot walk past the
// terminal state.
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
	if approvalID == "" {
		t.Fatalf("no approval ID: %v", err)
	}

	webApp, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}
	rec := postJSON(t, webApp, "/api/approvals/reject",
		`{"id":"`+approvalID+`","approver":"ui-operator"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/approvals/reject = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	// The gated task must be marked REJECTED immediately (F-026): a fresh
	// service (simulating `kern task`) sees the terminal state.
	fresh := app.NewTaskService(p, nil)
	got, ok := fresh.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not found in the persisted store", task.ID)
	}
	if got.State != domain.TaskRejected {
		t.Fatalf("state = %q, want REJECTED after UI rejection", got.State)
	}

	resumed, err := webApp.taskSvc.RunWorkflowResume(task.ID)
	if err == nil {
		t.Fatal("rejected workflow must not complete")
	}
	if resumed == nil {
		t.Fatal("resume returned nil task")
	}
	// A rejected task is terminal: the engine must not walk past the gate.
	if resumed.State != domain.TaskRejected {
		t.Fatalf("state = %s, want REJECTED after rejection (terminal)", resumed.State)
	}
}

// TestWebApprovalAdvancesGatedTask pins the approve-surface symmetry fix
// (F-026): POST /api/approvals/approve must route through the app-layer
// TaskService so a gated task parked at WAITING_FOR_APPROVAL advances to
// APPROVED immediately (visible via `kern task`) and the gate-crossing
// transition lands in the audit chain — mirroring `kern approve` and
// kern_approve. Before the fix the web surface only decided the approval,
// leaving the task parked until the next workflow resume.
func TestWebApprovalAdvancesGatedTask(t *testing.T) {
	root := fixtureRoot(t)

	// A workflow run parks at its human approval gate.
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
	if task.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %q, want WAITING_FOR_APPROVAL", task.State)
	}

	// The web console built over the SAME root.
	webApp, err := New(root)
	if err != nil {
		t.Fatalf("web.New: %v", err)
	}

	// A human approves through the UI.
	rec := postJSON(t, webApp, "/api/approvals/approve",
		`{"id":"`+approvalID+`","approver":"ui-operator"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/approvals/approve = %d (%s), want 200", rec.Code, rec.Body.String())
	}

	// The gated task must have advanced IMMEDIATELY (no resume needed): a
	// fresh service (simulating `kern task`) sees APPROVED.
	fresh := app.NewTaskService(p, nil)
	got, ok := fresh.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not found in the persisted store", task.ID)
	}
	if got.State != domain.TaskApproved {
		t.Fatalf("state = %q, want APPROVED after UI approval", got.State)
	}

	// F-025: the gate-crossing transition must be in the persisted audit chain.
	entries, err := webApp.taskSvc.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	var crossed *governance.AuditEntry
	for i := range entries {
		if entries[i].Action == "task.transition" &&
			strings.Contains(entries[i].Reason, string(domain.TaskWaitingApproval)) &&
			strings.Contains(entries[i].Reason, string(domain.TaskApproved)) {
			crossed = &entries[i]
		}
	}
	if crossed == nil {
		t.Fatalf("no WAITING_FOR_APPROVAL -> APPROVED audit entry; entries: %+v", entries)
	}

	// The workflow engine must still treat the (already-APPROVED) gate as
	// satisfied and drive the run to completion.
	resumed, err := webApp.taskSvc.RunWorkflowResume(task.ID)
	if err != nil {
		t.Fatalf("RunWorkflowResume after UI approval: %v", err)
	}
	if string(resumed.State) != "COMPLETED" {
		t.Fatalf("state = %s, want COMPLETED after UI approval", resumed.State)
	}
}
