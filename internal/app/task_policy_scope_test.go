package app

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestUnifiedTaskPolicyScope verifies the unified task policy: one
// TaskScope gates context, memory, artifacts, and runtime uniformly. A value
// denied for `context` must be denied for `memory`, `artifact`, and `runtime`
// under the SAME task and policy — there is exactly one boundary, not four.
func TestUnifiedTaskPolicyScope(t *testing.T) {
	ts := NewTaskService(&Platform{root: t.TempDir()}, nil)

	// Register a single task scope. Any value under the denied path is outside
	// the boundary regardless of the resource kind it is accessed through.
	ts.SetTaskScope("task-1", domain.TaskScope{
		TaskID:      "task-1",
		Paths:       []string{"src/"},
		DeniedPaths: []string{"src/secret/"},
		Envs:        []string{"development"},
	})

	kinds := []string{"context", "memory", "artifact", "runtime"}

	// A value denied for context is denied for every resource kind, and every
	// denial names the SAME boundary (the task scope), not a per-kind policy.
	denied := "src/secret/creds.go"
	for _, kind := range kinds {
		ok, reason := ts.authorizeResource(context.Background(), "task-1", kind, "read", denied)
		if ok {
			t.Errorf("%s: authorizeResource(%q) allowed, want deny", kind, denied)
		}
		if reason == "" {
			t.Errorf("%s: expected a deny reason for %q", kind, denied)
		}
		if !strings.Contains(reason, "outside the task scope") {
			t.Errorf("%s: reason %q does not name the shared task boundary", kind, reason)
		}
	}

	// A value inside the scope is allowed across all resource kinds.
	allowed := "src/services/util.go"
	for _, kind := range kinds {
		ok, _ := ts.authorizeResource(context.Background(), "task-1", kind, "read", allowed)
		if !ok {
			t.Errorf("%s: authorizeResource(%q) denied, want allow", kind, allowed)
		}
	}

	// The environment gate applies uniformly too: an action that targets an
	// out-of-scope environment is denied for every resource kind.
	for _, kind := range kinds {
		ok, _ := ts.authorizeResource(context.Background(), "task-1", kind, "read:production", allowed)
		if ok {
			t.Errorf("%s: authorizeResource(read:production) allowed, want deny", kind)
		}
	}
}

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
