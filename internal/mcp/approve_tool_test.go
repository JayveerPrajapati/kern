package mcp

import (
	"strings"
	"testing"
	"time"

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
