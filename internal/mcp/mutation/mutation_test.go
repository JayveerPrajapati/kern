package mutation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMutationDryRun(t *testing.T) {
	ctx := context.Background()
	res, err := Test(ctx, map[string]any{
		"dry_run": "true",
	})
	if err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if !strings.Contains(res, "Mutation Testing Report") {
		t.Errorf("expected report header, got: %s", res)
	}
}

// mutationFixture writes a minimal Go module with one mutatable condition
// (if a > b) into dir.
func mutationFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/calc\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "calc.go"), []byte("package calc\n\nfunc Greater(a, b int) bool {\n\tif a > b {\n\t\treturn true\n\t}\n\treturn false\n}\n"), 0o644); err != nil {
		t.Fatalf("write calc.go: %v", err)
	}
}

// TestMutationTestCommandDeniedWithoutExecAllowlist locks the exec-gate
// requirement (finding: kern_mutate_test's test_command executed arbitrary
// client-supplied commands with NO governance gate): a client-supplied
// test_command must fail closed without KERN_ALLOW_EXEC=1 or a KERN_TOOLS
// allowlist naming the tool — the same gate kern_exec/kern_sandbox pass.
func TestMutationTestCommandDeniedWithoutExecAllowlist(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_EXEC_RISK", "")
	dir := t.TempDir()
	mutationFixture(t, dir)

	_, err := Test(context.Background(), map[string]any{
		"root":         dir,
		"files":        "calc.go",
		"test_command": "go test ./...",
	})
	if err == nil {
		t.Fatal("test_command must be denied without KERN_ALLOW_EXEC=1 or an exec allowlist")
	}
	if !strings.Contains(err.Error(), "command execution blocked") {
		t.Fatalf("expected the governance exec-gate denial, got: %v", err)
	}
}

// TestMutationTestCommandDeniedByUnrelatedAllowlist pins that an allowlist
// naming another exec tool (kern_exec) does NOT re-enable kern_mutate_test's
// exec surface — the tool must be named explicitly.
func TestMutationTestCommandDeniedByUnrelatedAllowlist(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "kern_exec")
	dir := t.TempDir()
	mutationFixture(t, dir)

	_, err := Test(context.Background(), map[string]any{
		"root":         dir,
		"files":        "calc.go",
		"test_command": "go test ./...",
	})
	if err == nil {
		t.Fatal("an allowlist that does not name kern_mutate_test must not re-enable its exec surface")
	}
	if !strings.Contains(err.Error(), "command execution blocked") {
		t.Fatalf("expected the governance exec-gate denial, got: %v", err)
	}
}

// TestMutationTestCommandScrubsOperatorEnv pins the env allowlist through the
// MCP surface: the spawned test process (env, exit 0 -> every evaluated
// mutant is survived with the child env captured in test_output) must not
// receive an operator sentinel var, while the allowlisted PATH still flows.
func TestMutationTestCommandScrubsOperatorEnv(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	const sentinel = "KERN_TEST_SECRET_9f3a2c7e_value"
	t.Setenv("KERN_TEST_SECRET", sentinel)

	dir := t.TempDir()
	mutationFixture(t, dir)

	res, err := Test(context.Background(), map[string]any{
		"root":         dir,
		"files":        "calc.go",
		"test_command": "env",
		"format":       "json",
	})
	if err != nil {
		t.Fatalf("Test with allowed env command: %v", err)
	}
	if !strings.Contains(res, "PATH=") {
		t.Errorf("expected the spawned process env (PATH=...) in the report, got: %.400s", res)
	}
	if strings.Contains(res, sentinel) {
		t.Errorf("spawned process received the operator env: KERN_TEST_SECRET leaked into %q", res)
	}
}

// TestMutationAllowedGoTestCommand pins that the allowed default command
// (go test ./...) still works through the MCP surface once the operator opts
// in via KERN_ALLOW_EXEC=1 — the gate must not break the legitimate path.
func TestMutationAllowedGoTestCommand(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "1")
	// Hermetic go env: the allowlist forwards exactly these vars.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOCACHE", filepath.Join(t.TempDir(), "gocache"))
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPROXY", "off")

	dir := t.TempDir()
	mutationFixture(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte("package calc\n\nimport \"testing\"\n\nfunc TestGreater(t *testing.T) {\n\tif !Greater(5, 3) {\n\t\tt.Errorf(\"expected 5 > 3 to be true\")\n\t}\n\tif Greater(3, 5) {\n\t\tt.Errorf(\"expected 3 > 5 to be false\")\n\t}\n}\n"), 0o644); err != nil {
		t.Fatalf("write calc_test.go: %v", err)
	}

	res, err := Test(context.Background(), map[string]any{
		"root":         dir,
		"files":        "calc.go",
		"test_command": "go test ./...",
	})
	if err != nil {
		t.Fatalf("allowed default test command through the MCP surface: %v", err)
	}
	if !strings.Contains(res, "Mutation Testing Report") {
		t.Errorf("expected a mutation report, got: %.400s", res)
	}
	if !strings.Contains(res, "Killed Mutants:") {
		t.Errorf("expected evaluated mutants in the report, got: %.400s", res)
	}
}
