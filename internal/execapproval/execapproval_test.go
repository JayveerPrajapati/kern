package execapproval

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func testApproval() domain.Approval {
	decided := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return domain.Approval{
		Kind:         "allowed-for-command",
		ID:           "appr-test123",
		TaskID:       "sha256:abcdef",
		Requester:    "agent-1",
		Approver:     "human-1",
		Status:       "approved",
		Reason:       "verified live",
		RequestedAt:  time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC),
		DecidedAt:    &decided,
		RiskLevel:    domain.RiskHigh,
		PolicyIDs:    []string{"pol-1", "pol-2"},
		EvidenceRefs: []string{"ev-1"},
	}
}

func testSecret(t *testing.T) []byte {
	t.Helper()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	return secret
}

func TestMACRoundTrip(t *testing.T) {
	a := testApproval()
	secret := testSecret(t)
	stamp, err := Stamp(a, secret)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	if !strings.HasPrefix(stamp, StampPrefix) {
		t.Fatalf("stamp %q lacks prefix %q", stamp, StampPrefix)
	}
	a.ArtifactID = stamp
	if !MACValid(a, secret) {
		t.Fatal("MACValid rejected a record stamped with the same secret")
	}
	wrong := testSecret(t)
	wrong[0] ^= 0xff
	if MACValid(a, wrong) {
		t.Fatal("MACValid accepted a record with the wrong secret")
	}
}

// TestMACTamperProof: any change to a decision-relevant field invalidates the
// stamp — the MAC covers the full record, not just (TaskID, Status, ID).
func TestMACTamperProof(t *testing.T) {
	secret := testSecret(t)
	base := testApproval()
	stamp, err := Stamp(base, secret)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	base.ArtifactID = stamp
	cases := map[string]func(*domain.Approval){
		"id":       func(a *domain.Approval) { a.ID = "other" },
		"status":   func(a *domain.Approval) { a.Status = "rejected" },
		"reason":   func(a *domain.Approval) { a.Reason = "misleading" },
		"approver": func(a *domain.Approval) { a.Approver = "attacker" },
	}
	for name, mutate := range cases {
		a := base
		mutate(&a)
		if MACValid(a, secret) {
			t.Errorf("tampered %s still validated", name)
		}
	}
}

// TestMACFramingInjective: the same bytes split across field boundaries must
// not verify — length-prefixed framing makes re-framing detectable.
func TestMACFramingInjective(t *testing.T) {
	secret := testSecret(t)
	a := testApproval()
	a.Requester = "ab"
	a.Approver = "c"
	stamp, err := Stamp(a, secret)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	a.ArtifactID = stamp
	b := testApproval()
	b.Requester = "a"
	b.Approver = "bc"
	if MACValid(b, secret) {
		t.Fatal("re-framed record (ab|c vs a|bc) verified — framing is not injective")
	}
}

func TestStampFrom(t *testing.T) {
	secret := testSecret(t)
	a := testApproval()
	stamp, err := Stamp(a, secret)
	if err != nil {
		t.Fatalf("Stamp: %v", err)
	}
	a.ArtifactID = stamp
	if got := StampFrom(a); got == StampPrefix+"" || len(got) != len(stamp)-len(StampPrefix) {
		t.Fatalf("StampFrom returned %q, want the hex MAC", got)
	}
	a.ArtifactID = "unrelated-artifact"
	if got := StampFrom(a); got != "" {
		t.Fatalf("StampFrom on unstamped record returned %q, want empty", got)
	}
}

func TestSecretSizeEnforced(t *testing.T) {
	a := testApproval()
	if _, err := MAC(a, make([]byte, 31)); err == nil {
		t.Fatal("MAC accepted an undersized secret")
	}
	if _, err := Stamp(a, make([]byte, 16)); err == nil {
		t.Fatal("Stamp accepted an undersized secret")
	}
}

func TestConsumedLedgerRoundTrip(t *testing.T) {
	secret := testSecret(t)
	path := filepath.Join(t.TempDir(), "nested", "exec-consumed.log")
	if err := AppendConsumedID(path, secret, "appr-1"); err != nil {
		t.Fatalf("AppendConsumedID: %v", err)
	}
	if err := AppendConsumedID(path, secret, "appr-2"); err != nil {
		t.Fatalf("AppendConsumedID: %v", err)
	}
	consumed, err := ConsumedIDs(path, secret)
	if err != nil {
		t.Fatalf("ConsumedIDs: %v", err)
	}
	if !consumed["appr-1"] || !consumed["appr-2"] || len(consumed) != 2 {
		t.Fatalf("consumed set = %v, want both IDs", consumed)
	}
	// A missing ledger is an empty set.
	missing, err := ConsumedIDs(filepath.Join(t.TempDir(), "absent.log"), secret)
	if err != nil {
		t.Fatalf("ConsumedIDs on missing ledger: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing ledger returned %v, want empty", missing)
	}
}

func TestConsumedLedgerFailsClosed(t *testing.T) {
	secret := testSecret(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "exec-consumed.log")

	// Corrupt line (no MAC) must error, not silently pass.
	if err := os.WriteFile(path, []byte("appr-bad\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ConsumedIDs(path, secret); err == nil {
		t.Fatal("corrupt ledger line did not fail closed")
	}

	// Forged entry (valid format, wrong secret) must error.
	if err := os.WriteFile(path, []byte("appr-x 0000000000000000000000000000000000000000000000000000000000000000\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ConsumedIDs(path, secret); err == nil {
		t.Fatal("forged ledger entry did not fail closed")
	}
}

// TestLegacyV2StampStillValidates pins the backward-compat contract of the
// v3 MAC: a record stamped by a pre-ask-protocol (v2) binary is NOT a
// forgery and must keep validating — the version-aware MACValid accepts
// both framings — while any field tamper on it still fails.
func TestLegacyV2StampStillValidates(t *testing.T) {
	a := testApproval()
	secret := testSecret(t)
	legacy, err := macCompute(a, secret, macLegacyVersion)
	if err != nil {
		t.Fatalf("macCompute v2: %v", err)
	}
	a.ArtifactID = StampPrefix + hex.EncodeToString(legacy)
	if !MACValid(a, secret) {
		t.Fatal("MACValid rejected a legacy v2-stamped record")
	}
	tampered := a
	tampered.Reason = a.Reason + " tampered"
	if MACValid(tampered, secret) {
		t.Fatal("MACValid accepted a tampered legacy record")
	}
	// New stamps are v3: current-format stamps validate too (round trip).
	fresh, err := Stamp(a, secret)
	if err != nil {
		t.Fatal(err)
	}
	a.ArtifactID = fresh
	if !MACValid(a, secret) {
		t.Fatal("MACValid rejected a current v3-stamped record")
	}
}
