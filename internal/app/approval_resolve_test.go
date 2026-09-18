package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// workflowFixtureRoot writes a minimal Go fixture so the platform can analyze
// a symbol during workflow runs.
func workflowFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module fixture\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"),
		"package main\n\n// NewServer returns a server.\nfunc NewServer() string { return \"s\" }\n")
	return root
}

// transitionReasons collects the from -> to reasons of every task.transition
// audit entry, in chain order.
func transitionReasons(entries []governance.AuditEntry) []string {
	var out []string
	for _, e := range entries {
		if e.Action == "task.transition" {
			out = append(out, e.Reason)
		}
	}
	return out
}

// assertTransitionSequence asserts the recorded transition reasons match the
// expected sequence in order (each expected entry is a substring of the
// recorded reason).
func assertTransitionSequence(t *testing.T, reasons []string, want []string) {
	t.Helper()
	if len(reasons) != len(want) {
		t.Fatalf("transition count = %d (%v), want %d (%v)", len(reasons), reasons, len(want), want)
	}
	for i, w := range want {
		if !strings.Contains(reasons[i], w) {
			t.Errorf("transition[%d] = %q, want it to contain %q (full: %v)", i, reasons[i], w, reasons)
		}
	}
}

func TestResolveApprovalForTaskAdvancesGatedTask(t *testing.T) {
	root := workflowFixtureRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	// Run the workflow; it must park at the human approval gate.
	task, err := ts.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require human approval before execution")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatal("no approval ID surfaced from the approval gate")
	}
	if task.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %q, want WAITING_FOR_APPROVAL", task.State)
	}

	// A fresh service (simulating a separate `kern task` process) captures the
	// pre-decision persisted state before the decision is made.
	fresh := NewTaskService(p, nil)
	pre, ok := fresh.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not found in the persisted store before the decision", task.ID)
	}
	preUpdated := pre.UpdatedAt

	// Approve through the app layer: the task must advance immediately.
	a, err := ts.ResolveApprovalForTask(approvalID, "human-1", true, "approved by test")
	if err != nil {
		t.Fatalf("ResolveApprovalForTask: %v", err)
	}
	if a.Status != "approved" {
		t.Fatalf("approval status = %q, want approved", a.Status)
	}

	// The persisted state must now reflect the decision: APPROVED with a
	// refreshed UpdatedAt.
	got, ok := fresh.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not found in the persisted store", task.ID)
	}
	if got.State != domain.TaskApproved {
		t.Fatalf("state = %q, want APPROVED after approve", got.State)
	}
	if !got.UpdatedAt.After(preUpdated) {
		t.Fatalf("UpdatedAt not refreshed by the decision: before=%v after=%v", preUpdated, got.UpdatedAt)
	}

	entries, err := fresh.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	var crossed *governance.AuditEntry
	var decision *governance.AuditEntry
	for i := range entries {
		switch {
		case entries[i].Action == "task.transition" &&
			strings.Contains(entries[i].Reason, string(domain.TaskWaitingApproval)) &&
			strings.Contains(entries[i].Reason, string(domain.TaskApproved)):
			crossed = &entries[i]
		case entries[i].Action == "approve" && entries[i].TaskID == task.ID:
			decision = &entries[i]
		}
	}
	if crossed == nil {
		t.Fatalf("no WAITING_FOR_APPROVAL -> APPROVED audit entry; entries: %+v", entries)
	}
	if decision == nil {
		t.Fatalf("no approve decision audit entry for task %s; entries: %+v", task.ID, entries)
	}
	if decision.AgentID != "human-1" || decision.Result != "approved" {
		t.Errorf("decision entry = %+v, want AgentID=human-1 Result=approved", *decision)
	}

	// The engine must still treat the (already-APPROVED) gate as satisfied
	// and drive the run to completion.
	task, err = ts.RunWorkflowResume(task.ID)
	if err != nil {
		t.Fatalf("RunWorkflowResume after out-of-band approve: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Fatalf("state = %q, want COMPLETED after resume", task.State)
	}
}

func TestResolveApprovalForTaskRejectMarksTaskRejected(t *testing.T) {
	root := workflowFixtureRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, err := ts.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require human approval")
	}
	approvalID := agent.ApprovalID(err)

	a, err := ts.ResolveApprovalForTask(approvalID, "human-2", false, "out of scope")
	if err != nil {
		t.Fatalf("ResolveApprovalForTask(reject): %v", err)
	}
	if a.Status != "rejected" {
		t.Fatalf("approval status = %q, want rejected", a.Status)
	}
	got, ok := ts.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not found", task.ID)
	}
	if got.State != domain.TaskRejected {
		t.Fatalf("state = %q, want REJECTED after reject", got.State)
	}

	entries, err := ts.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	var decision *governance.AuditEntry
	for i := range entries {
		if entries[i].Action == "reject" && entries[i].TaskID == task.ID {
			decision = &entries[i]
			break
		}
	}
	if decision == nil {
		t.Fatalf("no reject decision audit entry for task %s; entries: %+v", task.ID, entries)
	}
	if decision.Result != "denied" || decision.AgentID != "human-2" {
		t.Errorf("decision entry = %+v, want Result=denied AgentID=human-2", *decision)
	}
}

func TestWorkflowLifecycleTransitionsAudited(t *testing.T) {
	root := workflowFixtureRoot(t)
	p, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := NewTaskService(p, nil).WithAgentID("test")

	task, err := ts.RunWorkflowDefault("NewServer")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require human approval")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatal("no approval ID surfaced")
	}

	// First run: CREATED -> ANALYZING -> PLANNING -> WAITING_FOR_APPROVAL
	// must be in the chain.
	entries, err := ts.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	assertTransitionSequence(t, transitionReasons(entries), []string{
		"CREATED -> ANALYZING",
		"ANALYZING -> PLANNING",
		"PLANNING -> WAITING_FOR_APPROVAL",
	})

	// Resolve the gate out-of-band and resume: the gate crossing plus the
	// post-approval lifecycle (up to COMPLETED) must follow in the chain.
	if _, err := ts.ResolveApprovalForTask(approvalID, "human-1", true, "ok"); err != nil {
		t.Fatalf("ResolveApprovalForTask: %v", err)
	}
	task, err = ts.RunWorkflowResume(task.ID)
	if err != nil {
		t.Fatalf("RunWorkflowResume: %v", err)
	}
	if string(task.State) != "COMPLETED" {
		t.Fatalf("state = %q, want COMPLETED", task.State)
	}

	entries, err = ts.AuditEntriesForTask(task.ID)
	if err != nil {
		t.Fatalf("AuditEntriesForTask: %v", err)
	}
	// The complete governance flow, in order, with no invented transitions
	// (the run ends at COMPLETED directly from PR_CREATED — no DEPLOYING/
	// OBSERVING steps exist in the default workflow).
	assertTransitionSequence(t, transitionReasons(entries), []string{
		"CREATED -> ANALYZING",
		"ANALYZING -> PLANNING",
		"PLANNING -> WAITING_FOR_APPROVAL",
		"WAITING_FOR_APPROVAL -> APPROVED",
		"APPROVED -> EXECUTING",
		"EXECUTING -> VERIFYING",
		"VERIFYING -> READY_FOR_PR",
		"READY_FOR_PR -> PR_CREATED",
		"PR_CREATED -> COMPLETED",
	})
}
