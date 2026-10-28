package governance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Machine-keyed MAC for exported evidence bundles (2026-10-28 campaign,
// closing the digest-only-seal gap): the bundle hash is SHA-256, so a
// workspace-level attacker could recompute it over tampered content and
// produce a self-consistent forged bundle. Stamping the bundle hash with
// the same out-of-workspace machine secret that backs exec approvals makes
// that impossible on the sealing machine. The domain-separation prefix
// ("kern-evidence-bundle-v1") keeps bundle stamps and exec-approval stamps
// from ever validating against each other. Verification stays possible for
// legacy (unstamped) bundles; a PRESENT stamp must validate — cross-machine
// review belongs to the ed25519 project signature (C4), not this key.

// StampMachineMAC returns the hex HMAC-SHA256 of data under the machine
// secret, framed for evidence bundles. Fails when the secret is
// unavailable (degraded machines export digest-only bundles).
func StampMachineMAC(data []byte) (string, error) {
	key, err := execApprovalSecret()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(macBytes(key, data)), nil
}

// MachineMACValid verifies a StampMachineMAC value. Fail closed: any
// secret error, malformed input, or mismatch is false.
func MachineMACValid(mac string, data []byte) bool {
	want, err := StampMachineMAC(data)
	if err != nil || mac == "" {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(want))
}

// macBytes is the domain-separated HMAC core shared by both stamp helpers.
func macBytes(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("kern-evidence-bundle-v1:"))
	m.Write(data)
	return m.Sum(nil)
}
