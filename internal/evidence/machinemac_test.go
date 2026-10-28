package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGenerateStampsMachineMAC pins the 2026-10-28 campaign fix: every
// generated bundle carries a machine-keyed seal (HMAC of the bundle hash
// under the out-of-workspace machine secret), so a workspace-level
// attacker can no longer forge a self-consistent bundle by recomputing the
// unkeyed digest. A stamped bundle must Verify on the sealing machine.
func TestGenerateStampsMachineMAC(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "agent-1", "task-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if b.MachineMAC == "" {
		t.Fatal("Generate produced no machine-keyed seal (MachineMAC empty)")
	}
	if err := b.Verify(); err != nil {
		t.Fatalf("stamped bundle must Verify on the sealing machine: %v", err)
	}
	// Round-trip: the stamp survives JSON.
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MachineMAC != b.MachineMAC {
		t.Fatalf("machine MAC lost in round-trip: %q vs %q", parsed.MachineMAC, b.MachineMAC)
	}
	if err := parsed.Verify(); err != nil {
		t.Fatalf("parsed stamped bundle must Verify: %v", err)
	}
}

// TestVerifyRejectsForgedMachineMAC pins the fail-closed rule: a tampered
// bundle whose digest was recomputed AND whose machine MAC was replaced by
// garbage (an attacker cannot compute a valid one without the machine
// secret) must fail verification with the machine-seal error.
func TestVerifyRejectsForgedMachineMAC(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "agent-1", "task-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Forger's move: alter content, recompute the digest, invent a MAC.
	b.TaskID = "tampered-task"
	b.BundleHash = computeBundleHash(b)
	b.MachineMAC = "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
	err = b.Verify()
	if err == nil {
		t.Fatal("forged bundle with an invalid machine MAC must fail Verify")
	}
	if !strings.Contains(err.Error(), "machine-keyed seal mismatch") {
		t.Fatalf("error should name the machine-keyed seal, got: %v", err)
	}
}

// TestVerifyLegacyDigestOnlyBundleStillVerifies pins soft migration: a
// bundle with NO machine MAC (every bundle exported before the keyed seal)
// still verifies — only bundles that CARRY a stamp are held to it.
func TestVerifyLegacyDigestOnlyBundleStillVerifies(t *testing.T) {
	root, ix := fixtureRoot(t)
	b, err := Generate(root, "agent-1", "task-1", ix)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b.MachineMAC = "" // simulate a legacy export
	if err := b.Verify(); err != nil {
		t.Fatalf("legacy digest-only bundle must still Verify: %v", err)
	}
}
