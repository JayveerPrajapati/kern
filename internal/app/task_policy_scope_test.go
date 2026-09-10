package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestExecuteEnforcesTaskBoundary verifies the exit gate: a controlled
// action (Execute) cannot bypass task-scoped governance. A patch touching a
// path outside the task's scope is rejected BEFORE any execution happens.
// Execute creates its own task internally; the test service uses a fresh store
// so the created task's ID is deterministically "t-1". The scope is registered
// for that ID before calling Execute.
func TestExecuteEnforcesTaskBoundary(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	svc, _ := newTestTaskService(t)

	// Execute's internal Create yields "t-1" on a fresh store; scope it.
	svc.SetTaskScope("t-1", domain.TaskScope{
		TaskID:      "t-1",
		Paths:       []string{"UserService", "tests/"},
		DeniedPaths: []string{"UserService/secret/"},
	})

	outOfScope := `diff --git a/payments/refund.go b/payments/refund.go
--- a/payments/refund.go
+++ b/payments/refund.go
@@ -1 +1 @@
-old
+new
`
	_, _, err := svc.Execute(outOfScope)
	if err == nil {
		t.Fatal("Execute(out-of-scope patch) accepted, want boundary denial")
	}
	if !strings.Contains(err.Error(), "outside the allowed boundary") {
		t.Errorf("error = %q, want boundary denial", err)
	}
	// The task must have failed (the boundary gate is authoritative).
	stored, _ := svc.store.Get("t-1")
	if stored.State != domain.TaskFailed {
		t.Errorf("task state = %s, want FAILED after boundary denial", stored.State)
	}

	// A denied sub-path is also rejected.
	deniedSub := `diff --git a/UserService/secret/keys.go b/UserService/secret/keys.go
--- a/UserService/secret/keys.go
+++ b/UserService/secret/keys.go
@@ -1 +1 @@
-old
+new
`
	svc.SetTaskScope("t-2", domain.TaskScope{
		TaskID:      "t-2",
		Paths:       []string{"UserService", "tests/"},
		DeniedPaths: []string{"UserService/secret/"},
	})
	_, _, err = svc.Execute(deniedSub)
	if err == nil || !strings.Contains(err.Error(), "outside the allowed boundary") {
		t.Errorf("Execute(denied sub-path) err = %v, want boundary denial", err)
	}
}
