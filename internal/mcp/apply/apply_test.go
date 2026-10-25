package apply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyEmptyEdits(t *testing.T) {
	ctx := context.Background()
	_, err := Apply(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "edits parameter is required") {
		t.Fatalf("expected error on empty edits, got: %v", err)
	}
}

func TestApplyEscapingPath(t *testing.T) {
	ctx := context.Background()
	_, err := Apply(ctx, Hooks{}, map[string]any{
		"edits": `[{"path": "../../outside.go", "content": "package main"}]`,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the project root") {
		t.Fatalf("expected path escaping error, got: %v", err)
	}
}

func TestApplyAbsoluteOutsideRoot(t *testing.T) {
	ctx := context.Background()
	_, err := Apply(ctx, Hooks{}, map[string]any{
		"root":  t.TempDir(),
		"edits": `[{"path": "/etc/passwd", "content": "root"}]`,
	})
	if err == nil || !strings.Contains(err.Error(), "outside the project root") {
		t.Fatalf("expected outside-root error, got: %v", err)
	}
}

func TestApplyUnsupportedEditsFormat(t *testing.T) {
	ctx := context.Background()
	_, err := Apply(ctx, Hooks{}, map[string]any{
		"edits": 42,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported edits format") {
		t.Fatalf("expected unsupported-format error, got: %v", err)
	}
}

// applyFixture writes a minimal compilable Go module into dir.
func applyFixture(t *testing.T, dir string) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testapply\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(A()) }\n"), 0o644)
}

// TestApplyDryRunPreview pins the agent-facing dry-run path: a valid edit is
// previewed (unified diff + receipt) and the live tree is untouched.
func TestApplyDryRunPreview(t *testing.T) {
	dir := t.TempDir()
	applyFixture(t, dir)
	before, _ := os.ReadFile(filepath.Join(dir, "a.go"))

	got, err := Apply(context.Background(), Hooks{}, map[string]any{
		"root":    dir,
		"dry_run": "true",
		"edits":   `[{"path": "a.go", "content": "package main\n\nfunc A() int { return 2 }\n"}]`,
		"format":  "json",
	})
	if err != nil {
		t.Fatalf("dry-run apply failed: %v", err)
	}
	if !strings.Contains(got, `"success": true`) || !strings.Contains(got, `"applied": false`) {
		t.Fatalf("dry-run should report success with applied=false, got: %s", got)
	}
	if !strings.Contains(got, `"receipt"`) {
		t.Fatalf("dry-run should mint a receipt, got: %s", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if string(before) != string(after) {
		t.Fatal("dry-run must not modify the live tree")
	}
}

// TestApplyEngineDefaultNotGated locks the documented carve-out (the engine's
// derived `go test` default is NOT gated by CheckExecCommand — kern_apply has
// no client-supplied command string, unlike refactor/mutation). The test=true
// path must still run without KERN_ALLOW_EXEC/KERN_TOOLS.
func TestApplyEngineDefaultNotGated(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_EXEC_RISK", "")
	dir := t.TempDir()
	applyFixture(t, dir)

	got, err := Apply(context.Background(), Hooks{}, map[string]any{
		"root":   dir,
		"test":   "true",
		"edits":  `[{"path": "a.go", "content": "package main\n\nfunc A() int { return 2 }\n"}]`,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("engine-derived test default must not be exec-gated: %v", err)
	}
	if !strings.Contains(got, `"success": true`) {
		t.Fatalf("expected apply success, got: %s", got)
	}
	gotFile, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(gotFile), "return 2") {
		t.Fatalf("edit was not applied: %s", gotFile)
	}
}

// TestApplyBuildFailLeavesTreeUntouched pins that an edit breaking compilation
// is rejected by the sandbox gate with the live tree untouched.
func TestApplyBuildFailLeavesTreeUntouched(t *testing.T) {
	dir := t.TempDir()
	applyFixture(t, dir)
	before, _ := os.ReadFile(filepath.Join(dir, "a.go"))

	got, err := Apply(context.Background(), Hooks{}, map[string]any{
		"root":  dir,
		"edits": `[{"path": "a.go", "content": "package main\n\nfunc Broken( {"}]`,
	})
	if err != nil {
		t.Fatalf("apply returned unexpected error: %v", err)
	}
	if !strings.Contains(got, "FAILED") {
		t.Fatalf("compile-breaking edit should report FAILED, got: %s", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if string(before) != string(after) {
		t.Fatal("failed apply must not modify the live tree")
	}
}
