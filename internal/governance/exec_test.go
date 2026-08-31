package governance

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestCheckExecFailsClosedOnEmptyAllowlist(t *testing.T) {
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
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
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec(); err != nil {
		t.Fatalf("CheckExec denied despite KERN_ALLOW_EXEC=1: %v", err)
	}
}

func TestCheckExecAllowedWithExecAllowlist(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_exec,kern_sandbox,kern_execute")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec(); err != nil {
		t.Fatalf("CheckExec denied despite an exec-tool allowlist: %v", err)
	}
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("CheckExec denied allowlisted tool kern_sandbox: %v", err)
	}
}

func TestCheckExecUnrelatedAllowlistDenied(t *testing.T) {
	// A non-empty allowlist naming only unrelated tools must NOT re-enable exec.
	t.Setenv("KERN_TOOLS", "kern_search,kern_plan")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec(); err == nil {
		t.Fatal("CheckExec allowed exec with an unrelated-only allowlist; want denial")
	}
	if err := CheckExec("kern_sandbox"); err == nil {
		t.Fatal("CheckExec allowed kern_sandbox despite it not being allowlisted; want denial")
	}
	if !strings.Contains(CheckExec("kern_sandbox").Error(), "kern_sandbox") {
		t.Fatal("denial should name the refused tool")
	}
}

func TestCheckExecToolNotAllowed(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec("kern_exec"); err == nil {
		t.Fatal("CheckExec allowed kern_exec despite it not being allowlisted; want denial")
	}
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("CheckExec denied the allowlisted tool kern_sandbox: %v", err)
	}
}

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
	// Default (unset) stays MEDIUM: not approval-gated, so the command runs.
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("default MEDIUM command should not require approval: %v", err)
	}
}

func TestCheckExecRiskDefaultMedium(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("default-risk command should run: %v", err)
	}
}

func TestCheckExecRiskInvalidValueDefaultsMedium(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "EXTREME")
	// Unrecognized value must fall back to MEDIUM and not gate approval.
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("invalid KERN_EXEC_RISK should default to MEDIUM and run: %v", err)
	}
}

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

	if err := ResumeExecApproval(wf, ap.ID); err == nil {
		t.Fatal("ResumeExecApproval allowed the command before approval; want denial")
	}
	if _, err := wf.Approve(ap.ID, "oncall-human"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if err := ResumeExecApproval(wf, ap.ID); err != nil {
		t.Fatalf("ResumeExecApproval after approval should be nil, got %v", err)
	}
}

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

func TestRequestExecApprovalBlockedNoAllowlist(t *testing.T) {
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	wf := NewApprovalWorkflow()
	if _, _, err := RequestExecApproval(wf); err == nil {
		t.Fatal("RequestExecApproval should fail closed without an allowlist or opt-in")
	}
}

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

func TestResumeExecApprovalUnknown(t *testing.T) {
	wf := NewApprovalWorkflow()
	if err := ResumeExecApproval(wf, "does-not-exist"); err == nil {
		t.Fatal("ResumeExecApproval for unknown approval should fail closed")
	}
}

func TestResumeExecApprovalNilWorkflow(t *testing.T) {
	if err := ResumeExecApproval(nil, "appr-1"); err == nil {
		t.Fatal("ResumeExecApproval with nil workflow should fail closed")
	}
}

func TestParseToolAllowlist(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ,, c ", []string{"a", "b", "c"}},
		{",,,", []string{}},
	}
	for _, c := range cases {
		got := parseToolAllowlist(c.in)
		if len(got) != len(c.want) {
			t.Errorf("parseToolAllowlist(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parseToolAllowlist(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestContainsString(t *testing.T) {
	list := []string{"kern_exec", "kern_sandbox"}
	if !containsString(list, "kern_exec") {
		t.Error("containsString should find an exact match")
	}
	if containsString(list, "kern") {
		t.Error("containsString should not do substring matching")
	}
	if containsString(list, "other") {
		t.Error("containsString should not match absent entries")
	}
	if containsString(nil, "x") {
		t.Error("containsString(nil) should be false")
	}
}
