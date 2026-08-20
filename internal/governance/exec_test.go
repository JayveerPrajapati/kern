package governance

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestCheckExecFailsClosedOnEmptyAllowlist(t *testing.T) {
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_ALLOW_EXEC", "")
	err := CheckExec()
	if err == nil {
		t.Fatal("CheckExec allowed execution with empty allowlist and no opt-in; want denial")
	}
	if !strings.Contains(err.Error(), "KERN_ALLOW_EXEC") {
		t.Fatalf("error should mention the opt-in env: %v", err)
	}
}

func TestCheckExecOptInAllows(t *testing.T) {
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_ALLOW_EXEC", "1")
	if err := CheckExec(); err != nil {
		t.Fatalf("CheckExec denied despite KERN_ALLOW_EXEC=1: %v", err)
	}
}

func TestCheckExecAllowedWithExecAllowlist(t *testing.T) {
	// KERN_TOOLS naming a real exec tool permits exec (no opt-in needed) — the
	// allowlist contents are validated, and this one is a genuine exec tool.
	t.Setenv("KERN_TOOLS", "kern_exec,kern_sandbox,kern_execute")
	t.Setenv("KERN_ALLOW_EXEC", "")
	if err := CheckExec(); err != nil {
		t.Fatalf("CheckExec denied despite an exec-tool allowlist: %v", err)
	}
	// A specific, allowlisted tool is also allowed.
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("CheckExec denied kern_sandbox despite it being allowlisted: %v", err)
	}
}

// TestCheckExecUnrelatedAllowlistDenied: a non-empty KERN_TOOLS that names only
// unrelated tools must NOT re-enable arbitrary host command execution. This is
// the bypass the original gate had (any non-empty value allowed exec).
func TestCheckExecUnrelatedAllowlistDenied(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_search,kern_plan")
	t.Setenv("KERN_ALLOW_EXEC", "")
	if err := CheckExec(); err == nil {
		t.Fatal("CheckExec allowed exec with an unrelated-only allowlist; want denial")
	}
	// A specific exec tool that is not in the allowlist is refused too.
	if err := CheckExec("kern_sandbox"); err == nil {
		t.Fatal("CheckExec allowed kern_sandbox despite it not being allowlisted; want denial")
	}
	if !strings.Contains(CheckExec("kern_sandbox").Error(), "kern_sandbox") {
		t.Fatal("denial should name the refused tool")
	}
}

// TestCheckExecToolNotAllowed denies a requested exec tool that is absent from
// an otherwise valid allowlist.
func TestCheckExecToolNotAllowed(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	if err := CheckExec("kern_exec"); err == nil {
		t.Fatal("CheckExec allowed kern_exec despite it not being allowlisted; want denial")
	}
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("CheckExec denied the allowlisted tool kern_sandbox: %v", err)
	}
}

// TestCheckExecHighRiskRequiresApproval a sensitive (operator-configurable)
// command.execute scores HIGH and therefore requires approval, failing closed
// rather than running ungoverned.
func TestCheckExecHighRiskRequiresApproval(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	err := CheckExec("kern_sandbox")
	if err == nil {
		t.Fatal("CheckExec allowed a HIGH-risk command without approval; want denial")
	}
	if !strings.Contains(err.Error(), "approval") {
		t.Fatalf("error should mention human approval: %v", err)
	}
	// Default (unset) KERN_EXEC_RISK stays MEDIUM: not approval-gated, so the
	// same command runs.
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("default MEDIUM command should not require approval: %v", err)
	}
}

// TestRequestExecApprovalHighWithWorkflow: a HIGH-risk command with an approval
// workflow configured returns a pending approval carrying an ID, and the command
// does not run until the human approves.
func TestRequestExecApprovalHighWithWorkflow(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	wf := NewApprovalWorkflow()
	ap, risk, err := RequestExecApproval(wf, "kern_sandbox")
	if err != nil {
		t.Fatalf("RequestExecApproval: %v", err)
	}
	if ap == nil {
		t.Fatal("expected a pending approval for a HIGH-risk command")
	}
	if ap.ID == "" {
		t.Fatal("expected the pending approval to carry an ID")
	}
	if ap.Status != "pending" {
		t.Fatalf("approval status = %q, want pending", ap.Status)
	}
	if risk.Level != domain.RiskHigh {
		t.Fatalf("risk level = %q, want HIGH", risk.Level)
	}

	// Before approval the command must not run.
	if err := ResumeExecApproval(wf, ap.ID); err == nil {
		t.Fatal("ResumeExecApproval allowed the command before approval; want denial")
	}

	// After the human approves, resume proceeds (command may run).
	if _, err := wf.Approve(ap.ID, "oncall-human"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := ResumeExecApproval(wf, ap.ID); err != nil {
		t.Fatalf("ResumeExecApproval after approval should be nil, got %v", err)
	}
}

// TestRequestExecApprovalHighNoWorkflow: a HIGH-risk command with NO approval
// workflow configured fails closed and never runs.
func TestRequestExecApprovalHighNoWorkflow(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	ap, _, err := RequestExecApproval(nil, "kern_sandbox")
	if err == nil {
		t.Fatal("RequestExecApproval with no workflow should fail closed for HIGH risk")
	}
	if ap != nil {
		t.Fatalf("expected nil approval when no workflow configured, got %+v", ap)
	}
	if !strings.Contains(err.Error(), "approval") {
		t.Fatalf("error should mention approval/failing closed: %v", err)
	}
}

// TestRequestExecApprovalMediumAllowed: a MEDIUM (default) command needs no
// approval and may proceed directly.
func TestRequestExecApprovalMediumAllowed(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")

	wf := NewApprovalWorkflow()
	ap, _, err := RequestExecApproval(wf, "kern_sandbox")
	if err != nil {
		t.Fatalf("RequestExecApproval denied a MEDIUM command: %v", err)
	}
	if ap != nil {
		t.Fatalf("expected no approval needed for MEDIUM, got %+v", ap)
	}
}

// TestResumeExecApprovalRejected: a rejected approval must not let the command
// run.
func TestResumeExecApprovalRejected(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	wf := NewApprovalWorkflow()
	ap, _, err := RequestExecApproval(wf, "kern_sandbox")
	if err != nil || ap == nil {
		t.Fatalf("RequestExecApproval: %v (ap=%v)", err, ap)
	}
	if _, err := wf.Reject(ap.ID, "oncall-human", "denied"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if err := ResumeExecApproval(wf, ap.ID); err == nil {
		t.Fatal("ResumeExecApproval allowed a rejected approval; want denial")
	}
}
