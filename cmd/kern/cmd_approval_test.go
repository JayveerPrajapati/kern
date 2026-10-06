package main

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

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

// TestApprovalWaitApproved locks the happy path: the wait command prints the
// pending state, blocks until the decision lands (decided in a goroutine
// ~100ms in), prints the approval, and exits 0.
func TestApprovalWaitApproved(t *testing.T) {
	root := newRoot(t)
	id := "wait-ok"
	seedPendingApproval(t, root, id)

	decided := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, err := governance.NewFileStore(root).Decide(id, "human", true, "looks good")
		decided <- err
	}()

	code := -1
	out := captureStdout(t, func() { code = runApproval([]string{"wait", id, "--root", root, "--timeout", "10s"}) })
	if err := <-decided; err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if code != 0 {
		t.Errorf("runApproval(wait) exit = %d, want 0", code)
	}
	for _, want := range []string{"pending: " + id, "approved: " + id} {
		if !strings.Contains(out, want) {
			t.Errorf("wait output missing %q:\n%s", want, out)
		}
	}
}

// TestApprovalWaitRejected locks the denied exit code: a rejection lands as
// rc=3 (the policy-outcome / denied convention), with the decision printed.
func TestApprovalWaitRejected(t *testing.T) {
	root := newRoot(t)
	id := "wait-no"
	seedPendingApproval(t, root, id)

	decided := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, err := governance.NewFileStore(root).Decide(id, "human", false, "not now")
		decided <- err
	}()

	code := -1
	out := captureStdout(t, func() { code = runApproval([]string{"wait", id, "--root", root, "--timeout", "10s"}) })
	if err := <-decided; err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if code != 3 {
		t.Errorf("runApproval(wait) exit = %d, want 3 (denied)", code)
	}
	if !strings.Contains(out, "rejected: "+id) {
		t.Errorf("wait output missing %q:\n%s", "rejected: "+id, out)
	}
}

// TestApprovalWaitTimeout locks the bounded wait: when --timeout expires
// before any decision lands, the command exits 1 promptly (a ~50ms timeout
// keeps the test fast) — it must never block past the bound.
func TestApprovalWaitTimeout(t *testing.T) {
	root := newRoot(t)
	id := "wait-slow"
	seedPendingApproval(t, root, id)

	start := time.Now()
	code := -1
	out := captureStdout(t, func() { code = runApproval([]string{"wait", id, "--root", root, "--timeout", "50ms"}) })
	elapsed := time.Since(start)
	if code != 1 {
		t.Errorf("runApproval(wait) exit = %d, want 1 (timeout)", code)
	}
	if !strings.Contains(out, "pending: "+id) {
		t.Errorf("wait output missing %q:\n%s", "pending: "+id, out)
	}
	if !strings.Contains(out, "timed out") {
		t.Errorf("wait output missing the timeout line:\n%s", out)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait took %s on a 50ms timeout — the wait must be bounded", elapsed)
	}
}

// TestApprovalBareUsageError locks the sub-dispatch: bare `kern approval` or
// an unknown subcommand is a usage error (rc=2), mirroring the strict
// sub-dispatch pattern.
func TestApprovalBareUsageError(t *testing.T) {
	expectExit(t, 2, func() { runApproval(nil) })
	expectExit(t, 2, func() { runApproval([]string{"bogus"}) })
}

// TestApprovalWaitUnknownID locks the fail-fast: waiting on an id that does
// not exist in the store is a real error (rc=1), never a silent block for
// the full timeout.
func TestApprovalWaitUnknownID(t *testing.T) {
	root := newRoot(t)
	stderr := captureStderr(t, func() {
		expectExit(t, 1, func() { runApproval([]string{"wait", "t-nonexistent", "--root", root, "--timeout", "2s"}) })
	})
	if !strings.Contains(stderr, "approval not found") || !strings.Contains(stderr, "already consumed") {
		t.Fatalf("unknown-id message should name both the miss and the consumed case, got: %q", stderr)
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
