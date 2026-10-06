package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/governance"
)

// execApprovalIDRe extracts the approval ID from a CheckExecCommand denial
// message ("... approval appr-<hex> pending — resolve with: kern approve ...").
// Both approval-ID generators (crypto/rand 16-hex and the monotonic
// fallbackApprovalID %016x) produce "appr-" + hex, so [0-9a-f]+ covers both.
var execApprovalIDRe = regexp.MustCompile(`approval (appr-[0-9a-f]+) pending`)

// execApprovalIDFromDeny extracts the minted approval ID from a governance
// denial error, failing the test when the expected deny shape is absent.
func execApprovalIDFromDeny(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatal("expected an approval-required denial, got nil")
	}
	m := execApprovalIDRe.FindStringSubmatch(err.Error())
	if len(m) != 2 {
		t.Fatalf("denial does not carry an approval ID (\"approval appr-<id> pending\"): %v", err)
	}
	return m[1]
}

// TestApprovalTriangleDenyApproveWaitRetry locks the full governed-bash
// approval triangle across the cmd/kern command surface and the governance
// gate, end to end in one fixture root:
//
//	deny    (CheckExecCommand mints a persisted, command-hash-bound approval) →
//	approve (`kern approve <id> --approver X --reason Y` decides)             →
//	wait    (`kern approval wait <id>` observes the decision, exit 0)         →
//	retry   (the SAME byte-identical command passes exactly once)             →
//
// plus the hash-binding pin (a DIFFERENT command mints a NEW approval and
// never passes on the first grant) and the compound pre-execution honesty
// note ("no segment of the compound has run").
//
// Env mirrors internal/governance/exec_test.go's approval-lifecycle tests
// (KERN_TOOLS allowlist + KERN_EXEC_RISK=HIGH, no KERN_ALLOW_EXEC). HOME and
// XDG_CONFIG_HOME are additionally redirected because the exec-approval HMAC
// secret and the consumed ledger are resolved via os.UserConfigDir — OUTSIDE
// the fixture root — and this package cannot reach the governance package's
// unexported overrideExecApprovalSecret; the redirect emulates it.
func TestApprovalTriangleDenyApproveWaitRetry(t *testing.T) {
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	// os.UserConfigDir → $HOME/Library/Application Support (darwin) or
	// $XDG_CONFIG_HOME else $HOME/.config (linux). Redirect both so the
	// exec secret + consumed ledger land under this test's temp dirs.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := newRoot(t) // XDG_CACHE_HOME redirected, isolated fixture root

	const cmd = "make install"

	// 1. MINT: a HIGH-risk command is denied by the governance gate, which
	// persists a command-hash-bound approval and names it in the error with
	// the byte-identical retry rule and the `kern approval wait <id>` hint.
	err := governance.CheckExecCommand(cmd, root)
	if err == nil {
		t.Fatal("CheckExecCommand allowed a HIGH-risk command without approval")
	}
	msg := err.Error()
	for _, want := range []string{
		"requires human approval",
		"approval appr-",
		"resolve with: kern approve",
		"byte-identical",
		"retry exactly: make install",
		"kern approval wait ",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("deny message missing %q:\n%s", want, msg)
		}
	}
	id := execApprovalIDFromDeny(t, err)

	// The minted approval must be persisted in the fixture root's store and
	// still pending (decidable out-of-band by `kern approve`).
	store := governance.NewFileStore(root)
	rec, gerr := store.Get(id)
	if gerr != nil {
		t.Fatalf("minted approval %s not persisted: %v", id, gerr)
	}
	if rec.Status != "pending" {
		t.Fatalf("minted approval status = %q, want pending", rec.Status)
	}

	// 2. DECIDE: `kern approve` with an approver + reason resolves the pending
	// exec approval out-of-band (scripted interactive confirmation types the
	// id, as the existing approval tests do).
	withScriptedTerminal(t, id)
	out := captureStdout(t, func() {
		runApprove([]string{"--root", root, "--approver", "senior", "--reason", "e2e approval", id})
	})
	for _, want := range []string{"approved: " + id, "approver: senior"} {
		if !strings.Contains(out, want) {
			t.Errorf("approve output missing %q:\n%s", want, out)
		}
	}

	// 3. OBSERVE: `kern approval wait <id>` observes the landed decision and
	// exits 0 (approved), printing the approver name.
	code := -1
	out = captureStdout(t, func() {
		code = runApproval([]string{"wait", id, "--root", root, "--timeout", "1s"})
	})
	if code != 0 {
		t.Errorf("runApproval(wait) exit = %d, want 0 (approved)", code)
	}
	for _, want := range []string{"pending: " + id, "approved: " + id, "approver: senior"} {
		if !strings.Contains(out, want) {
			t.Errorf("wait output missing %q:\n%s", want, out)
		}
	}

	// 4. CONSUME: the byte-identical command now passes the gate exactly once
	// (the grant is single-use and bound to the command's SHA-256), and the
	// grant is removed from the store.
	if err := governance.CheckExecCommand(cmd, root); err != nil {
		t.Fatalf("byte-identical command after approval must pass once, got %v", err)
	}
	if _, err := store.Get(id); err == nil {
		t.Fatal("approved grant must be consumed (record removed from the store)")
	}

	// 5. HASH-BINDING PIN: a DIFFERENT command never passes on the first
	// grant — it is denied again and mints a NEW approval id.
	otherCmd := "make install 2>&1 | tail -3"
	err2 := governance.CheckExecCommand(otherCmd, root)
	if err2 == nil {
		t.Fatal("a different command must not pass on the first grant")
	}
	id2 := execApprovalIDFromDeny(t, err2)
	if id2 == id {
		t.Fatalf("different command must mint a NEW approval, reused %s", id)
	}
	for _, want := range []string{"approval appr-", "byte-identical", "retry exactly: make install 2>&1 | tail -3", "kern approval wait "} {
		if !strings.Contains(err2.Error(), want) {
			t.Errorf("second deny message missing %q:\n%s", want, err2)
		}
	}

	// 6. Compound honesty note: a denied compound (&& / || / ;) states the
	// denial is pre-execution — no segment of the compound has run. Feasible
	// in-process: same gate, fresh mint.
	err3 := governance.CheckExecCommand("make install && echo done", root)
	if err3 == nil {
		t.Fatal("compound HIGH-risk command must be denied")
	}
	if !strings.Contains(err3.Error(), "no segment of the compound has run") {
		t.Errorf("compound denial missing the pre-execution note:\n%s", err3)
	}
}
