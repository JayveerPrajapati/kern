package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/app"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// TestApproveToolLifecycle exercises kern_approve end-to-end through the MCP
// surface: list-pending (empty), list-pending (non-empty), approve, reject,
// and default-approver behavior. Mirrors the `kern approve` CLI semantics.
func TestApproveToolLifecycle(t *testing.T) {
	root := mcpProject(t)
	store := governance.NewFileStore(root)

	// 1. No pending approvals. The server stamps index provenance onto every
	// tool call that loads the index (which kern_approve now does via the
	// TaskService), so assert on the handler text, not the full response.
	out := mcpLastOK(t, "kern_approve", map[string]any{"root": root})
	if !strings.HasPrefix(out, "no pending approvals") {
		t.Fatalf("expected 'no pending approvals', got: %s", out)
	}

	// 2. Add a pending approval; list should surface it.
	approval1 := domain.Approval{
		ID:          "appr-001",
		TaskID:      "t-1001",
		Requester:   "agent-coder",
		Status:      "pending",
		Reason:      "deploy to production",
		RequestedAt: time.Now(),
	}
	if err := store.AddPending(approval1); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	out = mcpLastOK(t, "kern_approve", map[string]any{"root": root})
	if !strings.Contains(out, "appr-001") || !strings.Contains(out, "t-1001") {
		t.Fatalf("list-pending missing approval:\n%s", out)
	}

	// 3. Approve it — default approver should be "mcp-user".
	out = mcpLastOK(t, "kern_approve", map[string]any{"root": root, "id": "appr-001"})
	if !strings.Contains(out, "approved: appr-001") {
		t.Fatalf("approve output missing 'approved: appr-001':\n%s", out)
	}
	if !strings.Contains(out, "mcp-user") {
		t.Fatalf("approve output missing default approver 'mcp-user':\n%s", out)
	}

	// 4. Add a second pending approval; reject it with a custom approver.
	approval2 := domain.Approval{
		ID:          "appr-002",
		TaskID:      "t-1002",
		Requester:   "agent-coder",
		Status:      "pending",
		Reason:      "risky change",
		RequestedAt: time.Now().Add(time.Second),
	}
	if err := store.AddPending(approval2); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	out = mcpLastOK(t, "kern_approve", map[string]any{
		"root":     root,
		"id":       "appr-002",
		"reject":   "true",
		"reason":   "too risky",
		"approver": "senior-eng",
	})
	if !strings.Contains(out, "rejected: appr-002") || !strings.Contains(out, "senior-eng") {
		t.Fatalf("reject output missing expected text:\n%s", out)
	}

	// 5. No more pending approvals.
	out = mcpLastOK(t, "kern_approve", map[string]any{"root": root})
	if !strings.HasPrefix(out, "no pending approvals") {
		t.Fatalf("expected 'no pending approvals' after resolving both, got: %s", out)
	}
}

// TestApproveToolNotFound verifies that approving a nonexistent ID returns an
// error rather than silently succeeding.
func TestApproveToolNotFound(t *testing.T) {
	root := mcpProject(t)
	resp := mcpCall(t, "kern_approve", map[string]any{"root": root, "id": "does-not-exist"})
	if e, ok := resp["error"].(map[string]any); ok {
		// RPC-level error is acceptable.
		_ = e
		return
	}
	text, isErr := toolResultText(t, resp)
	if !isErr {
		t.Fatalf("expected error for nonexistent approval ID, got: %s", text)
	}
	if !strings.Contains(text, "not found") {
		t.Fatalf("error text should mention 'not found': %s", text)
	}
}

// TestApproveToolAdvancesGatedTask pins the approve-surface symmetry fix
// (F-026): a kern_approve on a workflow approval must advance the gated task
// parked at WAITING_FOR_APPROVAL exactly like `kern approve` does — the task
// state flips immediately, the tool result carries the CLI's resume hint, and
// the gate-crossing transition lands in the audit chain. Before the fix the
// MCP surface only decided the approval, leaving the task parked.
func TestApproveToolAdvancesGatedTask(t *testing.T) {
	root := mcpProject(t)

	// Run the workflow through the app layer; it must park at the human
	// approval gate with a persisted approval (same setup as the app-level
	// and web tests).
	p, err := app.New(root)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	ts := app.NewTaskService(p, nil).WithAgentID("test")
	task, err := ts.RunWorkflowDefault("Greet")
	if err == nil {
		t.Fatal("RunWorkflowDefault should require human approval before execution")
	}
	approvalID := agent.ApprovalID(err)
	if approvalID == "" {
		t.Fatalf("no approval ID surfaced from the approval gate: %v", err)
	}
	if task.State != domain.TaskWaitingApproval {
		t.Fatalf("state = %q, want WAITING_FOR_APPROVAL", task.State)
	}

	// Approve through the MCP tool (default approver "mcp-user").
	out := mcpLastOK(t, "kern_approve", map[string]any{"root": root, "id": approvalID})
	if !strings.Contains(out, "approved: "+approvalID) {
		t.Fatalf("approve output missing 'approved: %s':\n%s", approvalID, out)
	}
	if !strings.Contains(out, "mcp-user") {
		t.Fatalf("approve output missing default approver 'mcp-user':\n%s", out)
	}
	// The CLI's resume hint must be present because a gated task was advanced.
	if !strings.Contains(out, "resume: kern workflow --task "+task.ID) {
		t.Fatalf("approve output missing resume hint for task %s:\n%s", task.ID, out)
	}

	// A fresh service (simulating `kern task`) must see the advanced state.
	fresh := app.NewTaskService(p, nil)
	got, ok := fresh.Get(task.ID)
	if !ok {
		t.Fatalf("task %s not found in the persisted store", task.ID)
	}
	if got.State != domain.TaskApproved {
		t.Fatalf("state = %q, want APPROVED after MCP approve", got.State)
	}

	// F-025: the gate-crossing transition must be in the persisted audit chain.
	entries, err := fresh.AuditEntriesForTask(task.ID)
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
}
