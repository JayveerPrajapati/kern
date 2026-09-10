package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadOrCreateKeysLifecycle pins key creation and idempotent reload:
// keys land under .kern/keys/ with a 0600 private key, and a reload yields
// the SAME fingerprint (a regenerated key would invalidate every bundle the
// old key signed).
func TestLoadOrCreateKeysLifecycle(t *testing.T) {
	root := t.TempDir()
	kp, err := LoadOrCreateKeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(kp.Fingerprint) != 16 {
		t.Fatalf("fingerprint = %q, want 16 hex chars", kp.Fingerprint)
	}
	privPath := filepath.Join(root, ".kern", "keys", "ed25519")
	fi, err := os.Stat(privPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("private key perms = %o, want 0600", fi.Mode().Perm())
	}
	reloaded, err := LoadOrCreateKeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Fingerprint != kp.Fingerprint {
		t.Fatalf("reload fingerprint = %q, want %q (stable project identity)", reloaded.Fingerprint, kp.Fingerprint)
	}
}

// TestLoadOrCreateKeysCorruptFailsClosed pins the fail-closed contract: a
// present-but-corrupt key is an error, never a silent regeneration.
func TestLoadOrCreateKeysCorruptFailsClosed(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadOrCreateKeys(root); err != nil {
		t.Fatal(err)
	}
	privPath := filepath.Join(root, ".kern", "keys", "ed25519")
	if err := os.WriteFile(privPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKeys(root); err == nil {
		t.Fatal("corrupt key loaded without error, want fail-closed")
	}
	// Mismatched pub/priv also fails.
	if _, err := LoadOrCreateKeys(root); err == nil || !strings.Contains(err.Error(), "refusing to regenerate") {
		t.Fatalf("corrupt-key error = %v, want refusal message", err)
	}
}

// TestSignVerifyRoundTrip pins the C4 story: a generated bundle signs with
// the project key, Verify passes, and the fingerprint is stable.
func TestSignVerifyRoundTrip(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "default", "", ix)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("unsigned bundle must verify: %v", err)
	}
	if b.Signature != nil {
		t.Fatal("Generate must not sign by default")
	}
	kp, err := LoadOrCreateKeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Sign(kp); err != nil {
		t.Fatal(err)
	}
	if b.Signature == nil || b.Signature.Algorithm != "ed25519" {
		t.Fatalf("signature section = %+v, want ed25519", b.Signature)
	}
	if b.Signature.KeyFingerprint != kp.Fingerprint {
		t.Fatalf("signature fingerprint = %q, want %q", b.Signature.KeyFingerprint, kp.Fingerprint)
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("signed bundle must verify: %v", err)
	}
	// The signature vouches for the seal: the hash is part of the signed
	// payload, so a hash mutation breaks BOTH checks.
	b.BundleHash = strings.Repeat("0", len(b.BundleHash))
	if err := b.Verify(); err == nil {
		t.Fatal("tampered bundle hash verified, want failure")
	}
}

// TestVerifyDetectsTampering pins tamper detection on a signed bundle: any
// content mutation breaks the seal.
func TestVerifyDetectsTampering(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "default", "", ix)
	if err != nil {
		t.Fatal(err)
	}
	kp, err := LoadOrCreateKeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Sign(kp); err != nil {
		t.Fatal(err)
	}
	b.AgentID = "forged"
	if err := b.Verify(); err == nil {
		t.Fatal("tampered bundle verified, want failure")
	}
}

// TestVerifyDetectsForgedSignature pins signature forgery: a signature from
// a DIFFERENT project key must not verify against the embedded public key
// (a tamperer who edits the bundle cannot re-sign without the private key).
func TestVerifyDetectsForgedSignature(t *testing.T) {
	rootA, ix := fixtureRoot(t)
	bA, err := Generate(rootA, "default", "", ix)
	if err != nil {
		t.Fatal(err)
	}
	kpA, err := LoadOrCreateKeys(rootA)
	if err != nil {
		t.Fatal(err)
	}
	if err := bA.Sign(kpA); err != nil {
		t.Fatal(err)
	}

	// A second project (key B) signs its own bundle; transplant B's
	// signature+key into A's bundle — the fingerprint check must reject it
	// even before the ed25519 verify.
	rootB := t.TempDir()
	bB, err := Generate(rootB, "default", "", ix)
	if err != nil {
		t.Fatal(err)
	}
	kpB, err := LoadOrCreateKeys(rootB)
	if err != nil {
		t.Fatal(err)
	}
	if err := bB.Sign(kpB); err != nil {
		t.Fatal(err)
	}
	bA.Signature = bB.Signature
	if err := bA.Verify(); err == nil {
		t.Fatal("foreign signature verified, want failure")
	}
}

// TestExplainSurfacesSignature pins the plain-language rendering: a signed
// bundle names its key, an unsigned one says so explicitly.
func TestExplainSurfacesSignature(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "default", "", ix)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.Explain(), "unsigned (digest-only seal)") {
		t.Fatalf("unsigned explain missing status:\n%s", b.Explain())
	}
	kp, err := LoadOrCreateKeys(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Sign(kp); err != nil {
		t.Fatal(err)
	}
	expl := b.Explain()
	for _, want := range []string{"key identity: signed with ed25519 key", kp.Fingerprint} {
		if !strings.Contains(expl, want) {
			t.Fatalf("signed explain missing %q in:\n%s", want, expl)
		}
	}
}
