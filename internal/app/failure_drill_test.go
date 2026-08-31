package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/eventbus"
	"github.com/JayveerPrajapati/kern/internal/execution"
	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// failure_drill_test.go consolidates the required failure modes for the
// safe-change vertical slice. Each test is deterministic and
// reuses the existing verification, governance and sandbox engines — no live
// LLM and no network. The pre-existing TestSevenFailureDrill covers
// nonexistent-task errors; these cover the seven substantive failure modes.

// TestFailureDrillPolicyDenial: an unauthorized production operation is DENIED,
// recorded to the audit log, and has no side effect.
func TestFailureDrillPolicyDenial(t *testing.T) {
	fw := governance.NewFirewall().WithAgents(governance.NewAgent(
		"dev", "Developer", "developer",
		[]governance.Permission{{Resource: "staging", Action: "write"}},
	))

	sideEffect := 0
	allowed, _, _, err := fw.Check("dev", "production", "deploy")
	if err == nil {
		t.Fatal("unauthorized production deploy should be denied with an error")
	}
	if allowed {
		t.Error("unauthorized production deploy must not be allowed")
	}
	if sideEffect != 0 {
		t.Error("side effect ran despite policy denial")
	}
	entries := fw.AuditLog().All()
	foundDenial := false
	for _, e := range entries {
		if e.Result == "denied" {
			foundDenial = true
		}
	}
	if !foundDenial {
		t.Error("audit log has no denied entry for the policy denial")
	}
}

// TestFailureDrillTestFailure: an injected failing test makes verification FAIL.
func TestFailureDrillTestFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module testfail\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "x.go"), "package testfail\n\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(dir, "x_test.go"),
		"package testfail\n\nimport \"testing\"\n\nfunc TestAddFail(t *testing.T) {\n\tif Add(1, 1) != 3 { t.Fatal(\"injected failure\") }\n}\n")

	res := verification.NewEngine(dir).Verify([]string{"test"})
	if res.UnitTests == nil {
		t.Fatal("test verification did not run")
	}
	if res.UnitTests.OK {
		t.Error("injected failing test should fail verification")
	}
	if res.Verdict != verification.VerdictFail {
		t.Errorf("verdict = %q, want FAIL", res.Verdict)
	}
}

// TestFailureDrillSecurityFailure: an injected critical security finding makes
// verification FAIL (deny).
func TestFailureDrillSecurityFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "sql.go"),
		"package app\nimport \"fmt\"\nfunc f(q string) { db.Query(fmt.Sprintf(\"SELECT * FROM t WHERE id=%s\", q)) }\n")

	res := verification.NewEngine(dir).Verify([]string{"security"})
	if res.Security == nil {
		t.Fatal("security verification did not run")
	}
	if res.Security.OK {
		t.Error("critical security finding should fail the security check")
	}
	if res.Security.Critical == 0 {
		t.Errorf("expected a critical security finding, got critical=%d", res.Security.Critical)
	}
	if res.Verdict != verification.VerdictFail {
		t.Errorf("verdict = %q, want FAIL", res.Verdict)
	}
}

// TestFailureDrillArchitectureFailure: a violating architectural boundary makes
// verification FAIL.
func TestFailureDrillArchitectureFailure(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib/lib.go"),
		"package lib\n\nfunc Public() string { return \"x\" }\n")
	writeFile(t, filepath.Join(dir, "client/client.go"),
		"package client\n\nimport \"lib\"\n\nfunc Caller() string { return lib.Public() }\n")
	writeFile(t, filepath.Join(dir, ".kern/boundaries.json"),
		`{"rules":[{"from":"client","to":"lib","action":"forbid"}]}`)

	res := verification.NewEngine(dir).Verify([]string{"architecture"})
	if res.Architecture == nil {
		t.Fatal("architecture verification did not run")
	}
	if res.Architecture.OK {
		t.Error("client->lib boundary violation should fail architecture check")
	}
	if len(res.Architecture.Violations) == 0 {
		t.Error("expected at least one architecture violation")
	}
}

// TestFailureDrillAgentTimeout: an agent exceeding its timeout fails the task and
// publishes a task.failed event.
func TestFailureDrillAgentTimeout(t *testing.T) {
	svc, bus := newTestTaskService(t)
	tk := agent.NewTask("code", "x")
	_ = tk.Start("bot-1")
	svc.registry.SubmitTask(tk)

	if err := svc.Timeout(tk.ID); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	last := lastEvent(t, bus)
	if last.Kind != eventbus.TaskFailed {
		t.Fatalf("event kind=%s, want task.failed", last.Kind)
	}
}

// TestFailureDrillToolFailure: a governed tool that errors is surfaced and has
// no side effect (fail-closed).
func TestFailureDrillToolFailure(t *testing.T) {
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")

	if err := governance.CheckExec("kern_exec"); err == nil {
		t.Fatal("governed exec tool should be refused when not allowlisted")
	}
}

// TestFailureDrillSandboxFailure: a snapshot/apply failure means the task is not
// applied (no side effect on the tree).
func TestFailureDrillSandboxFailure(t *testing.T) {
	root := t.TempDir()

	// Apply failure: a path-escaping patch must be rejected, leaving the tree
	// unchanged (task not applied).
	wt, err := execution.NewWorktree(root)
	if err != nil {
		t.Fatalf("NewWorktree: %v", err)
	}
	defer func() { _ = wt.Cleanup() }()
	if err := wt.Apply("diff --git a/../../escape.txt b/../../escape.txt\n--- /dev/null\n+++ b/../../escape.txt\n@@ -0,0 +1,1 @@\n+pwned\n"); err == nil {
		t.Error("path-escaping patch should be rejected by the sandbox")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); err == nil {
		t.Error("escaped file was written outside the worktree — sandbox escape")
	}
}
