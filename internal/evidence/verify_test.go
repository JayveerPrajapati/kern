package evidence

import (
	"strings"
	"testing"
)

// signedFixture returns a generated, project-signed bundle plus the signing
// key pair (its Fingerprint is the trust anchor a caller would supply).
func signedFixture(t *testing.T) (*Bundle, *KeyPair) {
	t.Helper()
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	kp, err := LoadOrCreateKeys(root)
	if err != nil {
		t.Fatalf("LoadOrCreateKeys: %v", err)
	}
	if err := b.Sign(kp); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return b, kp
}

// TestVerifyWithAnchor_SelfAttested pins the security-audit finding: a signed
// bundle verified WITHOUT an expected fingerprint is SELF-ATTESTED — the key
// identity is checked against the bundle-embedded key only, which a
// self-consistent attacker can satisfy. The result must say so explicitly.
func TestVerifyWithAnchor_SelfAttested(t *testing.T) {
	b, _ := signedFixture(t)
	res, err := b.VerifyWithAnchor("")
	if err != nil {
		t.Fatalf("VerifyWithAnchor(\"\"): %v", err)
	}
	if !res.SelfAttested {
		t.Error("SelfAttested = false, want true (no expected fingerprint supplied)")
	}
	if res.TrustAnchor != TrustAnchorSelfAttested {
		t.Errorf("TrustAnchor = %q, want %q", res.TrustAnchor, TrustAnchorSelfAttested)
	}
	if res.ExpectedFingerprint != "" {
		t.Errorf("ExpectedFingerprint = %q, want empty", res.ExpectedFingerprint)
	}
}

// TestVerifyWithAnchor_Anchored pins the anchored path: a matching expected
// fingerprint (the human trust anchor) turns the verification into an anchored
// decision, not a self-attestation.
func TestVerifyWithAnchor_Anchored(t *testing.T) {
	b, kp := signedFixture(t)
	res, err := b.VerifyWithAnchor(kp.Fingerprint)
	if err != nil {
		t.Fatalf("VerifyWithAnchor(%q): %v", kp.Fingerprint, err)
	}
	if res.SelfAttested {
		t.Error("SelfAttested = true, want false (expected fingerprint matched)")
	}
	if res.TrustAnchor != TrustAnchorAnchored {
		t.Errorf("TrustAnchor = %q, want %q", res.TrustAnchor, TrustAnchorAnchored)
	}
	if res.ExpectedFingerprint != kp.Fingerprint {
		t.Errorf("ExpectedFingerprint = %q, want %q", res.ExpectedFingerprint, kp.Fingerprint)
	}
}

// TestVerifyWithAnchor_Mismatch pins the no-regression contract: a mismatching
// expected fingerprint fails verification exactly as before (the CLI exit-2
// path), never a self-attested success.
func TestVerifyWithAnchor_Mismatch(t *testing.T) {
	b, kp := signedFixture(t)
	wrong := strings.Repeat("0", len(kp.Fingerprint))
	if wrong == kp.Fingerprint {
		wrong = strings.Repeat("1", len(kp.Fingerprint))
	}
	res, err := b.VerifyWithAnchor(wrong)
	if err == nil {
		t.Fatal("VerifyWithAnchor(mismatched fp) = nil error, want failure")
	}
	if res != nil {
		t.Errorf("VerifyWithAnchor returned result %+v on mismatch, want nil", res)
	}
	if !strings.Contains(err.Error(), "does not match expected") {
		t.Errorf("mismatch error = %v, want 'does not match expected'", err)
	}
}

// TestVerifyWithAnchor_Unsigned pins the digest-only path: an unsigned bundle
// has no key identity to anchor, so it is neither self-attested nor anchored.
func TestVerifyWithAnchor_Unsigned(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	res, err := b.VerifyWithAnchor("")
	if err != nil {
		t.Fatalf("VerifyWithAnchor(\"\") on unsigned bundle: %v", err)
	}
	if res.SelfAttested {
		t.Error("SelfAttested = true for an unsigned bundle, want false")
	}
	if res.TrustAnchor != "" {
		t.Errorf("TrustAnchor = %q, want empty for unsigned bundle", res.TrustAnchor)
	}
}

// TestVerifyWithAnchor_UnsignedWithAnchor pins the no-regression contract for
// the CLI: an unsigned bundle never satisfies an expected fingerprint.
func TestVerifyWithAnchor_UnsignedWithAnchor(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "default", "T-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := b.VerifyWithAnchor("deadbeef"); err == nil {
		t.Fatal("VerifyWithAnchor(fp) on unsigned bundle = nil error, want failure")
	}
}

// TestVerifyWithAnchor_Tampered pins that the anchor check never bypasses the
// tamper seal: content tampering fails verification even with a matching
// expected fingerprint.
func TestVerifyWithAnchor_Tampered(t *testing.T) {
	b, kp := signedFixture(t)
	b.Authorization.Proof.Decision.Allowed = !b.Authorization.Proof.Decision.Allowed
	if _, err := b.VerifyWithAnchor(kp.Fingerprint); err == nil {
		t.Fatal("VerifyWithAnchor(matching fp) on tampered bundle = nil error, want failure")
	}
}
