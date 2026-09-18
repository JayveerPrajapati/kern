package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func writeTestFile(root, name, content string) error {
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, name), []byte(content), 0o644)
}

// TestExecuteDirectPathFromCreated verifies the task-native direct execute
// path: a fresh CREATED task may transition straight to EXECUTING (gated by
// governance + task scope BEFORE the transition).
// Regression for the missing CREATED -> EXECUTING edge in the task state
// machine (internal/agent/task.go validTransitions), which made every
// `kern execute <patch>` / kern_execute MCP call fail with
// "invalid transition CREATED -> EXECUTING" — the direct path creates a task
// and executes it in one shot, it never walks analyze->plan->approve.
func TestExecuteDirectPathFromCreated(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module verify\n\ngo 1.23\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(root, "main.go", "package main\n\nfunc main() {\n\t_ = \"old\"\n}\n"); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(&Platform{root: root}, nil)

	patch := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,5 @@
 package main

 func main() {
-	_ = "old"
+	_ = "new"
 }
`

	task, _, err := svc.Execute(patch)
	if err != nil {
		t.Fatalf("Execute: direct CREATED->EXECUTING path failed: %v", err)
	}
	if task.State != domain.TaskCompleted {
		t.Fatalf("task state = %s, want COMPLETED", task.State)
	}

	// ExecuteAndVerify (the CLI kern execute path) must work too.
	svc2 := NewTaskService(&Platform{root: root}, nil)
	task2, _, _, err := svc2.ExecuteAndVerify(patch, nil)
	if err != nil {
		t.Fatalf("ExecuteAndVerify: direct path failed: %v", err)
	}
	if task2.State != domain.TaskCompleted {
		t.Fatalf("task2 state = %s, want COMPLETED", task2.State)
	}
}

// TestExecuteGarbagePatchClassifiedInvalid pins the invalid-patch error
// classification: a patch that cannot be applied to the worktree (garbage,
// malformed, or non-applicable input) must be returned wrapped in
// ErrInvalidPatch so callers (the web API) can map it to a client error (400)
// instead of a server error (500). Regression for the QA-reproduced 500 on
// POST /v1/execute with a garbage patch ("this is not a patch at all").
func TestExecuteGarbagePatchClassifiedInvalid(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := t.TempDir()
	if err := writeTestFile(root, "go.mod", "module verify\n\ngo 1.23\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(root, "main.go", "package main\n\nfunc main() {\n\t_ = \"old\"\n}\n"); err != nil {
		t.Fatal(err)
	}
	svc := NewTaskService(&Platform{root: root}, nil)

	_, _, err := svc.Execute("this is not a patch at all")
	if err == nil {
		t.Fatal("Execute(garbage patch) returned nil error, want ErrInvalidPatch-wrapped error")
	}
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("Execute(garbage patch) error = %v, want errors.Is(err, ErrInvalidPatch)", err)
	}
	// The git apply failure text must be preserved inside the wrapped error.
	if !strings.Contains(err.Error(), "apply patch failed") {
		t.Fatalf("Execute(garbage patch) error %q must retain the git apply failure text", err)
	}

	// ExecuteAndVerify (the CLI kern execute path) must classify the same way.
	_, _, _, err = svc.ExecuteAndVerify("this is not a patch at all", nil)
	if err == nil {
		t.Fatal("ExecuteAndVerify(garbage patch) returned nil error, want ErrInvalidPatch-wrapped error")
	}
	if !errors.Is(err, ErrInvalidPatch) {
		t.Fatalf("ExecuteAndVerify(garbage patch) error = %v, want errors.Is(err, ErrInvalidPatch)", err)
	}
}
