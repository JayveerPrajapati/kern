package refactor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransactionEmptyEdits(t *testing.T) {
	ctx := context.Background()
	_, err := Transaction(ctx, Hooks{}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "edits parameter is required") {
		t.Fatalf("expected error on empty edits, got: %v", err)
	}
}

func TestTransactionEscapingPath(t *testing.T) {
	ctx := context.Background()
	_, err := Transaction(ctx, Hooks{}, map[string]any{
		"edits": `[{"path": "../../outside.go", "content": "package main"}]`,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the project root") {
		t.Fatalf("expected path escaping error, got: %v", err)
	}
}

// refactorFixture writes a minimal compilable Go module into dir.
func refactorFixture(t *testing.T, dir string) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testtx\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(A()) }\n"), 0o644)
}

// TestTransactionCompileCommandDeniedWithoutExecAllowlist locks the exec-gate
// requirement (finding: kern_refactor_transaction's compile_command executed
// arbitrary client-supplied commands with NO governance gate): a
// client-supplied compile_command must fail closed without KERN_ALLOW_EXEC=1
// or a KERN_TOOLS allowlist naming the tool — the same gate
// kern_exec/kern_sandbox pass.
func TestTransactionCompileCommandDeniedWithoutExecAllowlist(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_EXEC_RISK", "")
	dir := t.TempDir()
	refactorFixture(t, dir)

	_, err := Transaction(context.Background(), Hooks{}, map[string]any{
		"root":            dir,
		"edits":           `[{"path": "a.go", "content": "package main\n\nfunc A() int { return 2 }\n"}]`,
		"compile_command": "go build ./...",
	})
	if err == nil {
		t.Fatal("compile_command must be denied without KERN_ALLOW_EXEC=1 or an exec allowlist")
	}
	if !strings.Contains(err.Error(), "command execution blocked") {
		t.Fatalf("expected the governance exec-gate denial, got: %v", err)
	}
}

// TestTransactionCompileCommandDeniedByUnrelatedAllowlist pins that an
// allowlist naming another exec tool (kern_exec) does NOT re-enable
// kern_refactor_transaction's exec surface — the tool must be named
// explicitly.
func TestTransactionCompileCommandDeniedByUnrelatedAllowlist(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "kern_exec")
	dir := t.TempDir()
	refactorFixture(t, dir)

	_, err := Transaction(context.Background(), Hooks{}, map[string]any{
		"root":            dir,
		"edits":           `[{"path": "a.go", "content": "package main\n\nfunc A() int { return 2 }\n"}]`,
		"compile_command": "go build ./...",
	})
	if err == nil {
		t.Fatal("an allowlist that does not name kern_refactor_transaction must not re-enable its exec surface")
	}
	if !strings.Contains(err.Error(), "command execution blocked") {
		t.Fatalf("expected the governance exec-gate denial, got: %v", err)
	}
}

// TestTransactionCompileCommandScrubsOperatorEnv pins the env allowlist
// through the MCP surface: the compile child (env, exit 0) must not receive
// an operator sentinel var, while the allowlisted PATH still flows.
func TestTransactionCompileCommandScrubsOperatorEnv(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	const sentinel = "KERN_TEST_SECRET_7f3a9c2e_value"
	t.Setenv("KERN_TEST_SECRET", sentinel)

	dir := t.TempDir()
	refactorFixture(t, dir)

	res, err := Transaction(context.Background(), Hooks{}, map[string]any{
		"root":            dir,
		"edits":           `[{"path": "a.go", "content": "package main\n\nfunc A() int { return 2 }\n"}]`,
		"compile_command": "env",
		"format":          "json",
	})
	if err != nil {
		t.Fatalf("Transaction with allowed env command: %v", err)
	}
	if !strings.Contains(res, "PATH=") {
		t.Errorf("expected the compile child env (PATH=...) in the result, got: %.400s", res)
	}
	if strings.Contains(res, sentinel) {
		t.Errorf("compile child received the operator env: KERN_TEST_SECRET leaked into %q", res)
	}
}

// TestTransactionAllowedCompileCommandWorks pins that the allowed default
// command (go build ./...) still works through the MCP surface once the
// operator opts in via KERN_ALLOW_EXEC=1 — the gate must not break the
// legitimate path, and the narrowed env must be sufficient for `go build`.
func TestTransactionAllowedCompileCommandWorks(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	// Hermetic go env: the allowlist forwards exactly these vars.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPROXY", "off")

	dir := t.TempDir()
	refactorFixture(t, dir)

	res, err := Transaction(context.Background(), Hooks{}, map[string]any{
		"root":            dir,
		"edits":           `[{"path": "a.go", "content": "package main\n\nfunc A() int { return 2 }\n"}]`,
		"compile_command": "go build ./...",
	})
	if err != nil {
		t.Fatalf("allowed go build through the MCP surface: %v", err)
	}
	if !strings.Contains(res, "✅ Transaction SUCCESSFUL") {
		t.Errorf("expected a successful transaction, got: %.400s", res)
	}
}
