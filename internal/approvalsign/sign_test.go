package approvalsign

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func signTestApproval() domain.Approval {
	return domain.Approval{
		Kind:         "allowed-for-command",
		ID:           "appr-test",
		TaskID:       "mcp-exec|command|execute|deadbeef",
		Requester:    "agent",
		Approver:     "operator",
		Status:       "approved",
		Reason:       "reviewed",
		RiskLevel:    "HIGH",
		Scope:        "once",
		Command:      "rm -rf build",
		MaxUses:      0,
		EvidenceRefs: []string{"rm -rf build"},
	}
}

func TestSignAndVerify(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := signTestApproval()
	sig, err := Sign(a, priv, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	a.Signature = sig
	if !SignatureValid(a, pub, "/repo") {
		t.Fatal("a valid signature must verify at its root")
	}
	if SignatureValid(a, pub, "/other-repo") {
		t.Fatal("a signature must not verify at another root")
	}
	tampered := a
	tampered.Command = "rm -rf /"
	if SignatureValid(tampered, pub, "/repo") {
		t.Fatal("changing the command must invalidate the signature")
	}
	tampered = a
	tampered.Status = "pending"
	if SignatureValid(tampered, pub, "/repo") {
		t.Fatal("changing the status must invalidate the signature")
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if SignatureValid(a, other.Public().(ed25519.PublicKey), "/repo") {
		t.Fatal("a different operator key must not verify")
	}
}

// TestSignatureExcludesUses pins the prefix-grant design: Uses is mutable at
// exec time (incremented without the operator present), so it is excluded from
// the signed message. Authorization fields remain covered.
func TestSignatureExcludesUses(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := signTestApproval()
	a.Scope = "prefix"
	a.Prefix = "rm -rf"
	a.MaxUses = 5
	a.Uses = 0
	sig, err := Sign(a, priv, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	a.Signature = sig
	a.Uses = 4
	if !SignatureValid(a, pub, "/repo") {
		t.Fatal("incrementing Uses must not invalidate the authorization signature")
	}
	a.MaxUses = 500
	if SignatureValid(a, pub, "/repo") {
		t.Fatal("widening MaxUses must invalidate the signature")
	}
}
