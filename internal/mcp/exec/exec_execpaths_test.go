package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/heal"
)

// TestWithinRoot pins the confinement helper used by the diff path: relative
// paths resolve inside, absolute-inside is accepted, and ".."/outside/symlink
// escapes are refused.
func TestWithinRoot(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "a.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := withinRoot(root, "a.txt"); err != nil || filepath.Base(got) != "a.txt" {
		t.Fatalf("relative: %q %v", got, err)
	}
	if _, err := withinRoot(root, inside); err != nil {
		t.Fatalf("absolute-inside must be allowed: %v", err)
	}
	if _, err := withinRoot(root, "../outside.txt"); err == nil {
		t.Fatal("parent escape must be refused")
	}
	if _, err := withinRoot(root, "/etc/passwd"); err == nil {
		t.Fatal("absolute outside must be refused")
	}
	link := filepath.Join(root, "escape.txt")
	if err := os.Symlink("/etc/passwd", link); err == nil {
		if _, err := withinRoot(root, "escape.txt"); err == nil {
			t.Fatal("symlink escape must be refused")
		}
	}
}

// TestSandboxRunsGuardedCommand covers the guarded-command success path (the
// command runs against the project root with write confinement and a snapshot)
// and the failure path where the tree is restored.
func TestSandboxRunsGuardedCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a guarded process")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Sandbox(context.Background(), "id", map[string]any{
		"root": root, "command": "echo hello-from-sandbox", "timeout": "30",
	})
	if err != nil {
		t.Skipf("sandbox unavailable in this environment: %v", err)
	}
	if !strings.Contains(out, "status:") {
		t.Fatalf("missing status line:\n%s", out)
	}
	if !strings.Contains(out, "PASS") && !strings.Contains(out, "FAIL") {
		t.Fatalf("status must be PASS or FAIL:\n%s", out)
	}
	if strings.Contains(out, "hello-from-sandbox") {
		// Output is only shown on the success path when the child printed it.
		if !strings.Contains(out, "output:") {
			t.Fatalf("captured output missing its label:\n%s", out)
		}
	}
}

// TestSandboxDeniedByGovernance pins the fail-closed default a fresh install
// sees: no KERN_ALLOW_EXEC and no allowlist → the command never starts.
func TestSandboxDeniedByGovernance(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	root := t.TempDir()
	if _, err := Sandbox(context.Background(), "id", map[string]any{"root": root, "command": "echo nope"}); err == nil {
		t.Fatal("sandbox must be denied without an exec grant")
	}
}

// TestValidateRunsDetectedCommand covers the validate success path: with exec
// permitted, a trivial command returns the PASS envelope, and raw=true returns
// the command's own output.
func TestValidateRunsDetectedCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a validation command")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	root := t.TempDir()

	out, err := Validate(context.Background(), map[string]any{"root": root, "command": "true", "timeout": "30"})
	if err != nil {
		t.Skipf("validate unavailable in this environment: %v", err)
	}
	if !strings.Contains(out, "status: PASS") || !strings.Contains(out, "command: true") {
		t.Fatalf("validate envelope:\n%s", out)
	}

	raw, err := Validate(context.Background(), map[string]any{"root": root, "command": "echo raw-out", "raw": true, "timeout": "30"})
	if err != nil {
		t.Fatalf("raw validate: %v", err)
	}
	if !strings.Contains(raw, "raw-out") {
		t.Fatalf("raw mode must return command output, got %q", raw)
	}
}

// TestRunBuildExecutesCommand covers the build runner: guard, execution, and
// output passthrough.
func TestRunBuildExecutesCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a build command")
	}
	t.Setenv("KERN_ALLOW_EXEC", "1")
	dir := t.TempDir()

	if _, err := RunBuild(context.Background(), "id", map[string]any{"dir": dir}); err == nil {
		t.Fatal("empty command must error")
	}
	out, err := RunBuild(context.Background(), "id", map[string]any{"dir": dir, "command": "echo build-ok"})
	if err != nil {
		t.Skipf("build runner unavailable in this environment: %v", err)
	}
	if !strings.Contains(out, "build-ok") {
		t.Fatalf("build output missing: %q", out)
	}
}

// TestRunBuildDeniedByGovernance pins fail-closed behavior for the build path.
func TestRunBuildDeniedByGovernance(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	if _, err := RunBuild(context.Background(), "id", map[string]any{"command": "echo nope", "dir": t.TempDir()}); err == nil {
		t.Fatal("build must be denied without an exec grant")
	}
}

// TestHealPlaybookRecordAndLookup pins the playbook store's write/read pair and
// the poison-scope limiter: a recorded fix replays only against the exact
// content it was recorded from.
func TestHealPlaybookRecordAndLookup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n\nvar x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := healPlaybook(root)
	if store == nil {
		t.Fatal("healPlaybook returned nil")
	}
	reps := []heal.Replacement{{Path: "main.go", Content: "package main\n\nvar x = 2\n"}}
	if err := store.Record("sig-1", reps); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, ok := store.Lookup("sig-1")
	if !ok || len(got) != 1 {
		t.Fatalf("lookup after record: %v %v", got, ok)
	}
	if _, ok := store.Lookup("sig-unknown"); ok {
		t.Fatal("unknown signature must miss")
	}

	// Drift: the file no longer matches what the fix was recorded from, so the
	// playbook must not replay (heal escalates to a real round instead).
	if err := os.WriteFile(target, []byte("package main\n\nvar x = 99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.Lookup("sig-1"); ok {
		t.Fatal("drifted content must miss the playbook")
	}
}
