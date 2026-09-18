package governance

import (
	"crypto/hmac"
	"path/filepath"
	"strings"
	"sync"
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
	ap, risk, err := RequestExecApproval(wf, "echo hello", "kern_sandbox")
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

	ap, _, err := RequestExecApproval(nil, "echo hello", "kern_sandbox")
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
	ap, _, err := RequestExecApproval(wf, "echo hello", "kern_sandbox")
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
	if _, _, err := RequestExecApproval(wf, "echo hello"); err == nil {
		t.Fatal("RequestExecApproval should fail closed without an allowlist or opt-in")
	}
}

func TestResumeExecApprovalRejected(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	wf := NewApprovalWorkflow()
	ap, _, err := RequestExecApproval(wf, "echo hello", "kern_sandbox")
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
		{"a,b,c", []string{"kern_a", "kern_b", "kern_c"}},
		{" a , b ,, c ", []string{"kern_a", "kern_b", "kern_c"}},
		{",,,", []string{}},
		// CLI aliases and MCP names normalize to the same canonical spelling.
		{"exec,kern_exec", []string{"kern_exec", "kern_exec"}},
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

// TestCheckExecCLIAliasAllowlist locks validation I-3: KERN_TOOLS entries may
// use CLI subcommand names ("exec", "sandbox", "execute") as aliases for the
// canonical MCP tool names — the gate normalizes both spellings, so CLI-driven
// automation is not footgunned by the naming asymmetry.
func TestCheckExecCLIAliasAllowlist(t *testing.T) {
	t.Setenv("KERN_TOOLS", "exec")
	if err := CheckExec("kern_exec"); err != nil {
		t.Fatalf("CLI alias 'exec' must allow kern_exec: %v", err)
	}
	if err := CheckExec(); err != nil {
		t.Fatalf("CLI alias 'exec' must satisfy the any-exec-tool gate: %v", err)
	}
	// Mixed styles must behave identically to their MCP-style spelling.
	t.Setenv("KERN_TOOLS", "sandbox,kern_search")
	if err := CheckExec("kern_sandbox"); err != nil {
		t.Fatalf("mixed alias allowlist must allow kern_sandbox: %v", err)
	}
	if err := CheckExec("kern_exec"); err == nil {
		t.Fatal("kern_exec must still be refused when only sandbox is allowed")
	}
	// A non-exec CLI alias must NOT re-enable exec (fail-closed preserved).
	t.Setenv("KERN_TOOLS", "validate,search")
	if err := CheckExec(); err == nil {
		t.Fatal("non-exec aliases must not re-enable execution")
	}
}

// TestNormalizeToolName pins the alias mapping itself.
func TestNormalizeToolName(t *testing.T) {
	for in, want := range map[string]string{
		"exec":      "kern_exec",
		"kern_exec": "kern_exec",
		"search":    "kern_search",
		"":          "",
		"kern_":     "kern_",
		"kernAudit": "kern_kernAudit",
	} {
		if got := NormalizeToolName(in); got != want {
			t.Errorf("NormalizeToolName(%q) = %q, want %q", in, got, want)
		}
	}
}

// approvalIDFromErr extracts the approval ID from a CheckExecCommand denial
// error ("...; approval appr-xxxx pending — resolve with: kern approve appr-xxxx").
func approvalIDFromErr(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an approval-required error")
	}
	msg := err.Error()
	const marker = "approval appr-"
	idx := strings.Index(msg, marker)
	if idx < 0 {
		t.Fatalf("error does not carry an approval ID: %v", err)
	}
	rest := msg[idx+len("approval "):]
	for i, r := range rest {
		if r == ' ' || r == '\n' || r == '\t' {
			return rest[:i]
		}
	}
	return rest
}

// overrideExecApprovalSecret redirects the HMAC secret to a temp dir and
// resets the package cache, so exec-approval tests never touch the real user
// config dir and are deterministic regardless of test order.
func overrideExecApprovalSecret(t *testing.T) {
	t.Helper()
	// Capture ONE temp dir per concern: t.TempDir() returns a NEW directory
	// on every call, so resolving the path inside the closure would make the
	// write and the read hit different files.
	secretDir := t.TempDir()
	orig := execApprovalSecretPath
	execApprovalSecretPath = func() (string, error) {
		return filepath.Join(secretDir, "exec-approval.key"), nil
	}
	execApprovalSecretOnce = sync.Once{}
	execApprovalSecretKey = nil
	execApprovalSecretErr = nil
	ledgerDir := t.TempDir()
	origLedger := execConsumedLedgerPath
	execConsumedLedgerPath = func() (string, error) {
		return filepath.Join(ledgerDir, "exec-consumed.log"), nil
	}
	t.Cleanup(func() {
		execApprovalSecretPath = orig
		execApprovalSecretOnce = sync.Once{}
		execApprovalSecretKey = nil
		execApprovalSecretErr = nil
		execConsumedLedgerPath = origLedger
	})
}

// TestExecApprovalMACFieldBoundaryInjective locks the length-prefix framing
// (oracle-gate): execApprovalMAC must be injective across field splits. Under
// bare 0x00 separators the same byte stream could be framed two ways —
// "ab"|"c" vs "a"|"bc" as a boundary split, and "ab\x00c" (one field
// containing a raw separator byte) vs "ab"|"c" (two fields) — producing
// identical MACs for different records. Length-prefixing each field/element
// makes every split produce a different MAC.
func TestExecApprovalMACFieldBoundaryInjective(t *testing.T) {
	overrideExecApprovalSecret(t)
	macOf := func(a domain.Approval) []byte {
		t.Helper()
		m, err := execApprovalMAC(a)
		if err != nil {
			t.Fatalf("execApprovalMAC: %v", err)
		}
		return m
	}
	cases := []struct {
		name string
		a, b domain.Approval
	}{
		{
			name: "ab|c vs a|bc boundary split",
			a:    domain.Approval{TaskID: "ab", Requester: "c", Status: "approved"},
			b:    domain.Approval{TaskID: "a", Requester: "bc", Status: "approved"},
		},
		{
			name: "embedded separator vs split at that byte",
			a:    domain.Approval{TaskID: "ab\x00c", Status: "approved"},
			b:    domain.Approval{TaskID: "ab", Requester: "c", Status: "approved"},
		},
		{
			name: "list element split (PolicyIDs)",
			a:    domain.Approval{TaskID: "x", PolicyIDs: []string{"ab", "c"}},
			b:    domain.Approval{TaskID: "x", PolicyIDs: []string{"a", "bc"}},
		},
	}
	for _, c := range cases {
		ma, mb := macOf(c.a), macOf(c.b)
		if hmac.Equal(ma, mb) {
			t.Errorf("%s: MACs must differ under length-prefixed framing (%x == %x)", c.name, ma, mb)
		}
	}
}

// TestCheckExecCommandApprovalLifecycle locks audit A4 + R2: the approval
// path is reachable — a HIGH/CRITICAL denial creates a PERSISTED,
// command-hash-bound, HMAC-stamped approval; after `kern approve <id>`
// (simulated via the shared store) the SAME command passes exactly ONCE (the
// grant is consumed), the next identical call creates a NEW pending
// approval, and a DIFFERENT command never passes on that approval.
func TestCheckExecCommandApprovalLifecycle(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	// 1. Denial creates a persisted, command-bound, integrity-stamped approval.
	err := CheckExecCommand("echo hello", root, "kern_sandbox")
	if err == nil {
		t.Fatal("CheckExecCommand allowed a HIGH-risk command without approval")
	}
	if !strings.Contains(err.Error(), "resolve with: kern approve") {
		t.Fatalf("denial should carry the kern approve hint: %v", err)
	}
	id := approvalIDFromErr(t, err)

	store := NewFileStore(root)
	a, gerr := store.Get(id)
	if gerr != nil {
		t.Fatalf("approval %s not persisted: %v", id, gerr)
	}
	if a.Status != "pending" {
		t.Fatalf("approval status = %q, want pending", a.Status)
	}
	if a.TaskID != execCommandKey("echo hello") {
		t.Fatalf("approval TaskID = %q, want command-bound key %q", a.TaskID, execCommandKey("echo hello"))
	}
	if len(a.EvidenceRefs) != 1 || a.EvidenceRefs[0] != "echo hello" {
		t.Fatalf("approval should carry the command text as evidence, got %v", a.EvidenceRefs)
	}
	if !execApprovalMACValid(a) {
		t.Fatalf("persisted approval must carry a valid integrity stamp (R1), got %+v", a)
	}

	// 2. A DIFFERENT command gets its own approval and stays denied.
	errB := CheckExecCommand("echo world", root, "kern_sandbox")
	if errB == nil {
		t.Fatal("a different command must not pass on the first approval")
	}
	if idB := approvalIDFromErr(t, errB); idB == id {
		t.Fatalf("different command must not reuse approval %s", id)
	}

	// 3. Approve id the way `kern approve <id>` does (shared store decision;
	// the re-stamp over the new status happens inside Decide).
	if _, derr := store.Decide(id, "oncall-human", true, ""); derr != nil {
		t.Fatalf("Decide: %v", derr)
	}
	if approved, gerr := store.Get(id); gerr != nil || approved.Status != "approved" {
		t.Fatalf("approved record = %+v, err=%v", approved, gerr)
	} else if !execApprovalMACValid(approved) {
		t.Fatalf("approved record must be re-stamped over its new status (R1), got %+v", approved)
	}

	// 4. Re-running the SAME command passes — exactly once.
	if err := CheckExecCommand("echo hello", root, "kern_sandbox"); err != nil {
		t.Fatalf("same command after approval must pass once, got %v", err)
	}

	// 5. The grant was CONSUMED (R2 single-use): the next identical call
	// creates a NEW pending approval instead of passing.
	err2 := CheckExecCommand("echo hello", root, "kern_sandbox")
	if err2 == nil {
		t.Fatal("approved command must be single-use: the next identical call must be pending again")
	}
	if id2 := approvalIDFromErr(t, err2); id2 == id {
		t.Fatalf("next identical call must create a NEW approval, not reuse %s", id)
	}

	// 6. A DIFFERENT command never passes on the first approval.
	if err := CheckExecCommand("echo world", root, "kern_sandbox"); err == nil {
		t.Fatal("a different command must not pass on the approved command's approval")
	}
}

// TestExecApprovalForgeryDenied locks R1: a client who can write the
// workspace's approvals.json but does not possess the exec secret (stored
// outside the workspace) cannot self-approve by forging records — neither a
// bare forged record nor a legitimate record with its Status flipped grants
// execution.
func TestExecApprovalForgeryDenied(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	// Create a real pending approval to learn the legitimate key shape.
	err := CheckExecCommand("echo secret-command", root, "kern_sandbox")
	if err == nil {
		t.Fatal("expected approval-required denial")
	}
	real, gerr := NewFileStore(root).Get(approvalIDFromErr(t, err))
	if gerr != nil {
		t.Fatalf("Get: %v", gerr)
	}

	// Forge 1: a bare record with the right key and Status approved, but no
	// valid HMAC stamp.
	store := NewFileStore(root)
	forged := domain.Approval{
		ID:        "appr-forged",
		TaskID:    real.TaskID,
		Status:    "approved",
		Requester: "governed-client",
		Reason:    "self-approved",
	}
	if ferr := store.AddPending(forged); ferr != nil {
		t.Fatalf("write forged record: %v", ferr)
	}
	if err := CheckExecCommand("echo secret-command", root, "kern_sandbox"); err == nil {
		t.Fatal("forged record must not grant execution (R1)")
	}

	// Forge 2: copy the legitimate pending record and flip Status to
	// approved — the HMAC covers Status, so the flip invalidates it.
	flipped := real
	flipped.ID = "appr-flipped"
	flipped.Status = "approved"
	if ferr := store.AddPending(flipped); ferr != nil {
		t.Fatalf("write status-flipped record: %v", ferr)
	}
	if err := CheckExecCommand("echo secret-command", root, "kern_sandbox"); err == nil {
		t.Fatal("status-flipped record must not grant execution (R1)")
	}
}

// TestCheckExecCommandNoRootHonestHint locks R6: with no project root the
// approval is in-memory only, so the error must NOT offer the out-of-band
// `kern approve` hint (a dead end) — it points at the allowlist instead.
func TestCheckExecCommandNoRootHonestHint(t *testing.T) {
	overrideExecApprovalSecret(t)
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")

	err := CheckExecCommand("echo hi", "", "kern_sandbox")
	if err == nil {
		t.Fatal("HIGH-risk command with no root must be denied")
	}
	if strings.Contains(err.Error(), "resolve with") {
		t.Fatalf("in-memory denial must not offer the out-of-band approve hint: %v", err)
	}
	if !strings.Contains(err.Error(), "KERN_TOOLS") {
		t.Fatalf("in-memory denial should point at the allowlist: %v", err)
	}
}

// TestCheckExecCommandMediumAllowed: default (MEDIUM) risk is not
// approval-gated, so the allowlist opt-in semantics are preserved.
func TestCheckExecCommandMediumAllowed(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "")
	if err := CheckExecCommand("echo hi", t.TempDir(), "kern_sandbox"); err != nil {
		t.Fatalf("MEDIUM command should run without approval: %v", err)
	}
}

// TestCheckExecCommandBlockedNoAllowlist: the allowlist gate still fails
// closed before any approval is created.
func TestCheckExecCommandBlockedNoAllowlist(t *testing.T) {
	t.Setenv("KERN_TOOLS", "")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	if err := CheckExecCommand("echo hi", t.TempDir()); err == nil {
		t.Fatal("CheckExecCommand should fail closed without an allowlist or opt-in")
	}
}

// TestExecApprovalTamperedReasonDenied locks gate-1 attempt-2: the HMAC
// covers the full decision-relevant record, so a governed client tampering a
// pending record's Reason (the text the human sees when approving) both
// fails the integrity check and makes the record UNDECIDABLE — Decide must
// refuse to re-stamp and launder the tamper into a valid grant.
func TestExecApprovalTamperedReasonDenied(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	err := CheckExecCommand("echo dangerous", root, "kern_sandbox")
	if err == nil {
		t.Fatal("expected approval-required denial")
	}
	id := approvalIDFromErr(t, err)
	store := NewFileStore(root)
	// Tamper the pending record's Reason through the raw file (what a
	// governed client with workspace write access could do). The stamp is
	// not recomputed, so the full-record MAC no longer verifies.
	approvals, lerr := store.Load()
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	found := false
	for i := range approvals {
		if approvals[i].ID == id {
			approvals[i].Reason = "routine build validation, safe to approve"
			found = true
		}
	}
	if !found {
		t.Fatalf("pending record %s not found", id)
	}
	if serr := store.Save(approvals); serr != nil {
		t.Fatalf("save: %v", serr)
	}
	tampered, gerr := store.Get(id)
	if gerr != nil {
		t.Fatalf("get: %v", gerr)
	}
	if ApprovalIntegrityOK(tampered) {
		t.Fatal("tampered record must fail the integrity check")
	}
	if _, derr := store.Decide(id, "oncall-human", true, ""); derr == nil {
		t.Fatal("Decide must refuse a tampered record")
	} else if !strings.Contains(derr.Error(), "integrity check failed") {
		t.Fatalf("Decide error should mention integrity, got: %v", derr)
	}
}

// TestExecApprovalReplayDenied locks R2d: consumption records the approval
// ID in the MAC'd ledger OUTSIDE the workspace, so a governed client that
// snapshots an approved record and re-inserts it after consumption cannot
// reauthorize the command.
func TestExecApprovalReplayDenied(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	err := CheckExecCommand("echo replay-me", root, "kern_sandbox")
	if err == nil {
		t.Fatal("expected approval-required denial")
	}
	id := approvalIDFromErr(t, err)
	store := NewFileStore(root)
	if _, derr := store.Decide(id, "oncall-human", true, ""); derr != nil {
		t.Fatalf("Decide: %v", derr)
	}
	// Snapshot the approved record (what a governed client could keep).
	approvals, lerr := store.Load()
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	var snapshot domain.Approval
	for _, a := range approvals {
		if a.ID == id {
			snapshot = a
		}
	}
	if snapshot.ID == "" {
		t.Fatal("approved record not found for snapshot")
	}
	// The same command passes once (claim consumes + ledgers).
	if err := CheckExecCommand("echo replay-me", root, "kern_sandbox"); err != nil {
		t.Fatalf("approved command must pass once, got %v", err)
	}
	// Replay: re-insert the snapshot verbatim (valid HMAC, Status approved).
	if aerr := store.AddPending(snapshot); aerr != nil {
		t.Fatalf("re-insert: %v", aerr)
	}
	if execApprovedFor(root, execCommandKey("echo replay-me")) != nil {
		t.Fatal("snapshot replay must be denied by the consumed ledger (R2d)")
	}
	if err := CheckExecCommand("echo replay-me", root, "kern_sandbox"); err == nil {
		t.Fatal("replayed approval must not grant execution")
	}
}

// TestFileStoreConsumeClaimsOnce locks the atomic single-use claim: Consume
// removes the record inside one flock'd mutation and reports true exactly
// once for a given ID.
func TestFileStoreConsumeClaimsOnce(t *testing.T) {
	store := NewFileStore(t.TempDir())
	a := domain.Approval{ID: "cons-1", TaskID: "task", Status: "approved", Reason: "test"}
	if err := store.AddPending(a); err != nil {
		t.Fatalf("AddPending: %v", err)
	}
	claimed, err := store.Consume("cons-1")
	if err != nil || !claimed {
		t.Fatalf("first Consume = (%v, %v), want (true, nil)", claimed, err)
	}
	claimed2, err := store.Consume("cons-1")
	if err != nil || claimed2 {
		t.Fatalf("second Consume = (%v, %v), want (false, nil)", claimed2, err)
	}
	if _, gerr := store.Get("cons-1"); gerr == nil {
		t.Fatal("consumed record must be removed from the store")
	}
}
