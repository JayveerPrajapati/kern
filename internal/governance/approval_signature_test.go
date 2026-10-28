package governance

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/execapproval"
)

// TestForgedHMACApprovalCannotGrant is the P0-3 regression (2026-10-28
// adversarial audit): a same-user process can read the HMAC key and recompute
// the v4 stamp over a mutated record. That forgery must NOT grant execution —
// the grant authority is the operator Ed25519 signature, which the attacker
// cannot produce without the passphrase.
func TestForgedHMACApprovalCannotGrant(t *testing.T) {
	overrideExecApprovalSecret(t)
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	root := t.TempDir()

	err := CheckExecCommand("echo forge-me", root, "kern_sandbox")
	if err == nil {
		t.Fatal("expected an approval-required denial")
	}
	id := approvalIDFromErr(t, err)
	const key = "echo forge-me"

	// The attack: read the HMAC secret (same-user readable), flip the record
	// to approved, and recompute the HMAC exactly as the audit did.
	secret, serr := execApprovalSecret()
	if serr != nil {
		t.Fatalf("read HMAC secret: %v", serr)
	}
	store := NewFileStore(root)
	approvals, lerr := store.Load()
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	found := false
	for i := range approvals {
		if approvals[i].ID != id {
			continue
		}
		now := time.Now()
		approvals[i].Status = "approved"
		approvals[i].Approver = "forged-agent"
		approvals[i].DecidedAt = &now
		stamp, sterr := execapproval.Stamp(approvals[i], secret, execRootIdentity(root))
		if sterr != nil {
			t.Fatalf("recompute HMAC: %v", sterr)
		}
		approvals[i].ArtifactID = stamp
		approvals[i].Signature = ""
		found = true
	}
	if !found {
		t.Fatalf("pending record %s not found", id)
	}
	if serr := store.Save(approvals); serr != nil {
		t.Fatalf("save forged store: %v", serr)
	}

	// The forged HMAC still validates, but it is not a grant: no signature.
	forged, gerr := store.Get(id)
	if gerr != nil {
		t.Fatalf("get forged: %v", gerr)
	}
	if !execApprovalMACValid(forged, execRootIdentity(root)) {
		t.Fatal("fixture invalid: the forged HMAC should validate (that is the attack)")
	}
	if execApprovedFor(root, execCommandKey(key)) != nil {
		t.Fatal("a forged HMAC-only approval must not grant execution (P0-3)")
	}
	if err := CheckExecCommand(key, root, "kern_sandbox"); err == nil {
		t.Fatal("the forged record must not let the command run")
	}

	// The legitimate path still works: a signed decision grants exactly once.
	approvals, lerr = store.Load()
	if lerr != nil {
		t.Fatalf("reload: %v", lerr)
	}
	var liveID string
	for _, a := range approvals {
		if a.TaskID == execCommandKey(key) && a.Status == "pending" {
			liveID = a.ID
		}
	}
	if liveID == "" {
		t.Fatal("no live pending approval after the forged record was denied")
	}
	if _, derr := store.DecideWithSigner(liveID, "oncall-human", true, "", testExecSigner(t)); derr != nil {
		t.Fatalf("signed Decide: %v", derr)
	}
	if execApprovedFor(root, execCommandKey(key)) == nil {
		t.Fatal("a signed approval must grant execution")
	}
	if err := CheckExecCommand(key, root, "kern_sandbox"); err != nil {
		t.Fatalf("signed approval must authorize the command once: %v", err)
	}
}

// TestExecApprovalRequiresInteractiveSignature pins the fail-closed decision
// path: without an operator signer an exec approval stays pending.
func TestExecApprovalRequiresInteractiveSignature(t *testing.T) {
	overrideExecApprovalSecret(t)
	t.Setenv("KERN_TOOLS", "kern_sandbox")
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_EXEC_RISK", "HIGH")
	root := t.TempDir()

	err := CheckExecCommand("echo sign-me", root, "kern_sandbox")
	if err == nil {
		t.Fatal("expected an approval-required denial")
	}
	id := approvalIDFromErr(t, err)
	store := NewFileStore(root)
	if _, derr := store.Decide(id, "oncall-human", true, ""); derr == nil {
		t.Fatal("Decide without a signer must fail closed for an exec approval")
	} else if !strings.Contains(derr.Error(), "interactive operator signature") {
		t.Fatalf("unexpected error: %v", derr)
	}
	rec, gerr := store.Get(id)
	if gerr != nil {
		t.Fatalf("get: %v", gerr)
	}
	if rec.Status != "pending" {
		t.Fatalf("record must stay pending after a refused signature, got %q", rec.Status)
	}
	if rec.Signature != "" {
		t.Fatal("a refused decision must not persist a signature")
	}
}
