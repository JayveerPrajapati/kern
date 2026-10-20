package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

// CLI-level dispatch tests for the approval/governance family. The family's
// implementation lives in internal/approvalcli; these tests exercise the
// commands THROUGH the commandTable dispatch path (the wiring contract the
// extraction must preserve), not the family internals (those tests moved to
// internal/approvalcli).

// seedPendingApproval writes a pending approval into <root>/.kern/approvals.json
// — the store `kern approval wait` polls.
func seedPendingApproval(t *testing.T, root, id string) {
	t.Helper()
	if err := governance.NewFileStore(root).AddPending(domain.Approval{
		ID:     id,
		TaskID: "t-wait",
		Status: "pending",
	}); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
}

// TestApprovalWaitDispatchRoutes locks the dispatch wiring end-to-end: the
// "approval" command table entry routes `kern approval wait` through
// dispatchCommand (including the F10 strict-flag validation) and maps the
// landed decision to rc=0.
func TestApprovalWaitDispatchRoutes(t *testing.T) {
	root := newRoot(t)
	id := "wait-dispatch"
	seedPendingApproval(t, root, id)

	decided := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, err := governance.NewFileStore(root).Decide(id, "human", true, "")
		decided <- err
	}()

	code := -1
	out := captureStdout(t, func() { code = dispatchCommand("approval", []string{"wait", id, "--root", root, "--timeout", "10s"}) })
	if err := <-decided; err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if code != 0 {
		t.Errorf("dispatchCommand(approval wait) exit = %d, want 0", code)
	}
	if !strings.Contains(out, "approved: "+id) {
		t.Errorf("dispatch output missing %q:\n%s", "approved: "+id, out)
	}
}

// TestRejectDispatchRoutesToRunReject locks the dispatch wiring of `kern
// reject`: the command table entry must route to approvalcli.RunReject (the
// two-store resolver shared with runApprove), not the bpcli single-store
// runner. An unknown id through the shared core fails with rc=1 (a plain
// returned code since the extraction — approvalcli recovers its internal
// sentinel); the bpcli runner would return 3 for the same miss.
func TestRejectDispatchRoutesToRunReject(t *testing.T) {
	e, ok := commandTable["reject"]
	if !ok {
		t.Fatal("commandTable has no reject entry")
	}
	if e.help == "" {
		t.Error("reject entry has empty help")
	}
	if code := dispatchCommand("reject", []string{"--root", t.TempDir(), "apr-nope"}); code != 1 {
		t.Errorf("dispatchCommand(reject unknown id) = %d, want 1 (the shared two-store resolver's not-found path; bpcli's single-store runner would return 3)", code)
	}
}
