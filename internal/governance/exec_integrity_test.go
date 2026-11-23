package governance

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// rotateMACKey replaces the exec-approval MAC secret on disk with a fresh key
// and drops the process cache — exactly what an operator key rotation does to
// every record stamped before it.
func rotateMACKey(t *testing.T) {
	t.Helper()
	p, err := execApprovalSecretPath()
	if err != nil {
		t.Fatalf("secret path: %v", err)
	}
	fresh := make([]byte, 32)
	for i := range fresh {
		fresh[i] = byte(i*7 + 1)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, fresh, 0o600); err != nil {
		t.Fatalf("write rotated key: %v", err)
	}
	execApprovalSecretOnce = sync.Once{}
	execApprovalSecretKey = nil
	execApprovalSecretErr = nil
}

// pinMACKeyAge backdates the MAC key so a record's RequestedAt is provably
// after key creation (the "not rotated" side of the discriminator).
func pinMACKeyAge(t *testing.T, age time.Duration) {
	t.Helper()
	p, err := execApprovalSecretPath()
	if err != nil {
		t.Fatalf("secret path: %v", err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(p, when, when); err != nil {
		t.Fatalf("backdate key: %v", err)
	}
}

func pendingExecRecord(id string, requestedAt time.Time) domain.Approval {
	return domain.Approval{
		ID:          id,
		Kind:        "allowed-for-command",
		Status:      "pending",
		Requester:   "agent",
		Reason:      "governed command",
		Command:     "make install",
		RiskLevel:   "medium",
		RequestedAt: requestedAt,
	}
}

func TestApprovalIntegrityUnstampedRecordIsOK(t *testing.T) {
	overrideExecApprovalSecret(t)
	a := domain.Approval{ID: "apr-task", TaskID: "t1", Status: "pending"}
	got := ApprovalIntegrity(a, "")
	if !got.OK() || got.Verdict != IntegrityOK {
		t.Fatalf("unstamped record must be OK, got %q (%s)", got.Verdict, got.Reason)
	}
}

func TestApprovalIntegrityKeyRotationIsNotTampering(t *testing.T) {
	overrideExecApprovalSecret(t)
	a := pendingExecRecord("appr-rotated", time.Now().Add(-time.Hour))
	if err := stampExecApproval("", &a); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if got := ApprovalIntegrity(a, ""); !got.OK() {
		t.Fatalf("freshly stamped record must verify, got %q (%s)", got.Verdict, got.Reason)
	}

	rotateMACKey(t)

	got := ApprovalIntegrity(a, "")
	if got.OK() || got.Verdict != IntegrityKeyRotated {
		t.Fatalf("record predating the key must read key-rotated, got %q (%s)", got.Verdict, got.Reason)
	}
	// Same failing MAC, but the record postdates the key: that IS tampering.
	a2 := a
	a2.RequestedAt = time.Now().Add(time.Second)
	if got := ApprovalIntegrity(a2, ""); got.Verdict != IntegrityTampered {
		t.Fatalf("record postdating the key with a bad MAC must read tampered, got %q (%s)", got.Verdict, got.Reason)
	}
}

func TestApprovalIntegrityTamperedRecordIsFlagged(t *testing.T) {
	overrideExecApprovalSecret(t)
	a := pendingExecRecord("appr-tampered", time.Now().Add(-time.Hour))
	if err := stampExecApproval("", &a); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	pinMACKeyAge(t, 2*time.Hour)
	a.Command = "rm -rf /"
	got := ApprovalIntegrity(a, "")
	if got.OK() || got.Verdict != IntegrityTampered {
		t.Fatalf("edited record must read tampered, got %q (%s)", got.Verdict, got.Reason)
	}
}

func TestApprovalIntegritySignatureUnverifiableOnKeyRotation(t *testing.T) {
	overrideExecApprovalSecret(t)
	signerA := NewExecSigner(func(bool) ([]byte, error) { return []byte("passphrase-alpha-1"), nil })
	now := time.Now()
	decided := now
	a := pendingExecRecord("appr-signed", now)
	a.Status = "approved"
	a.DecidedAt = &decided
	sig, err := signerA(a, "")
	if err != nil {
		t.Fatalf("sign with key A: %v", err)
	}
	a.Signature = sig
	if got := ApprovalIntegrity(a, ""); !got.OK() {
		t.Fatalf("signature under the current key must verify, got %q (%s)", got.Verdict, got.Reason)
	}

	// Rotate the signing key: a second envelope with a different passphrase is
	// created at the same path, invalidating every earlier signature.
	p, err := execApprovalSigningKeyPath()
	if err != nil {
		t.Fatalf("signing path: %v", err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove envelope: %v", err)
	}
	signerB := NewExecSigner(func(bool) ([]byte, error) { return []byte("passphrase-bravo-2"), nil })
	if _, err := signerB(pendingExecRecord("appr-signed-b", now), ""); err != nil {
		t.Fatalf("create key B: %v", err)
	}

	got := ApprovalIntegrity(a, "")
	if got.OK() || got.Verdict != IntegritySignatureUnverifiable {
		t.Fatalf("record signed by the old key must read signature-unverifiable, got %q (%s)", got.Verdict, got.Reason)
	}

	unsigned := pendingExecRecord("appr-approved-unsigned", now)
	unsigned.Status = "approved"
	if got := ApprovalIntegrity(unsigned, ""); got.Verdict != IntegritySignatureUnverifiable {
		t.Fatalf("approved-without-signature must read signature-unverifiable, got %q (%s)", got.Verdict, got.Reason)
	}
}

func TestRotateExecApprovalMACKeyKeepsBackupAndInvalidates(t *testing.T) {
	overrideExecApprovalSecret(t)
	root := t.TempDir()
	a := pendingExecRecord("appr-rotate-key", time.Now().Add(-time.Hour))
	if err := stampExecApproval(root, &a); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	keyPath, err := execApprovalSecretPath()
	if err != nil {
		t.Fatalf("secret path: %v", err)
	}
	backup, err := RotateExecApprovalMACKey()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if backup == "" {
		t.Fatal("rotate must return the backup path (never delete key material)")
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Errorf("old key still at the live path after rotation")
	}

	// Between rotation and the first new stamp the key is absent: the record
	// must read key-rotated (never tampered).
	if got := ApprovalIntegrity(a, root).Verdict; got != IntegrityKeyRotated {
		t.Fatalf("with the key absent the record must read key-rotated, got %q", got)
	}
	if _, err := execApprovalSecret(); err != nil {
		t.Fatalf("a fresh key must mint after rotation: %v", err)
	}
	if got := ApprovalIntegrity(a, root).Verdict; got != IntegrityKeyRotated {
		t.Fatalf("after the fresh key mints the old record must still read key-rotated, got %q", got)
	}
	counts, err := ApprovalIntegrityCounts(root)
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts[IntegrityKeyRotated] != 1 || counts[IntegrityTampered] != 0 {
		t.Fatalf("counts = %v, want exactly 1 key-rotated and 0 tampered", counts)
	}
}
