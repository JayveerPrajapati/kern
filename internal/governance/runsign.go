package governance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// runManifestPurpose domain-separates run-manifest stamps from exec-approval
// stamps: a stamp minted for one can never verify as the other, even though
// both use the same machine-bound exec secret.
var runManifestPurpose = []byte("kern-run-manifest\x00")

// StampRunManifest HMACs a verification-run manifest payload (canonical
// bytes chosen by the caller) with the machine-bound exec secret, bound to
// the canonical root identity exactly like an exec approval. The stamp is
// hex; an unavailable secret is an error (callers treat it as "no reuse").
func StampRunManifest(root string, payload []byte) (string, error) {
	key, err := execApprovalSecret()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(runManifestPurpose)
	mac.Write([]byte(execRootIdentity(root)))
	mac.Write([]byte{0})
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifyRunManifest reports whether stamp is a valid run-manifest HMAC for
// payload under root. Missing/malformed stamps and foreign-root stamps
// deny (fail closed). Like execApprovalMACValid this is integrity, not
// authorization: the HMAC key is same-user readable.
func VerifyRunManifest(root string, payload []byte, stamp string) bool {
	key, err := execApprovalSecret()
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(runManifestPurpose)
	mac.Write([]byte(execRootIdentity(root)))
	mac.Write([]byte{0})
	mac.Write(payload)
	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(stamp))
}
