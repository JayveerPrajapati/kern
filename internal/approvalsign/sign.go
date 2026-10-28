// Signing primitives for exec approvals (P0-3, 2026-10-28 adversarial audit).
//
// The original stamp was an HMAC keyed by a same-user-readable file: any agent
// that could read <UserConfigDir>/kern/exec-approval.key could modify a pending
// record, set Status=approved, and recompute a valid stamp. The grant authority
// is now an Ed25519 signature whose PRIVATE key is encrypted at rest with an
// operator passphrase entered only at the interactive approval prompt. Exec
// time verifies with the public key, so a same-user process cannot forge an
// approval even though it can read the key file.
package approvalsign

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strconv"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// signingVersion domain-separates the Ed25519 signing message from the HMAC
// framing, so a signature can never be replayed as an HMAC input (or vice
// versa).
const signingVersion byte = 5

// signingField writes the same injective length-prefixed framing used by the
// HMAC (v4), so field-boundary reframing cannot preserve a signature.
func signingField(b *bytes.Buffer, s string) {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	b.Write(lenBuf[:])
	b.WriteString(s)
	b.WriteByte(0)
}

func signingList(b *bytes.Buffer, xs []string) {
	for _, x := range xs {
		var lenBuf [4]byte
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(x)))
		b.Write(lenBuf[:])
		b.WriteString(x)
		b.WriteByte(1)
	}
	b.WriteByte(0)
}

// SigningMessage returns the exact bytes an operator signs. It covers every
// decision-relevant field including the canonical root identity. Uses is
// deliberately EXCLUDED: a prefix grant's use counter is incremented
// non-interactively at exec time, and the signature must keep verifying for
// the grant's lifetime. The HMAC (which still covers Uses) protects the
// counter's integrity; the signature protects the authorization itself, so
// flipping Scope/Prefix/Command/MaxUses/ExpiresAt/Status/root breaks it.
func SigningMessage(a domain.Approval, rootID string) []byte {
	var b bytes.Buffer
	b.WriteByte(signingVersion)
	signingField(&b, rootID)
	signingField(&b, a.ID)
	signingField(&b, a.TaskID)
	signingField(&b, a.Requester)
	signingField(&b, a.Approver)
	signingField(&b, a.Status)
	signingField(&b, a.Reason)
	signingField(&b, a.RequestedAt.UTC().Format(time.RFC3339Nano))
	if a.DecidedAt != nil {
		signingField(&b, a.DecidedAt.UTC().Format(time.RFC3339Nano))
	} else {
		signingField(&b, "")
	}
	signingField(&b, string(a.RiskLevel))
	signingList(&b, a.PolicyIDs)
	signingList(&b, a.EvidenceRefs)
	signingField(&b, a.Scope)
	signingField(&b, a.Prefix)
	signingField(&b, a.Command)
	if a.ExpiresAt.IsZero() {
		signingField(&b, "")
	} else {
		signingField(&b, a.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	signingField(&b, strconv.Itoa(a.MaxUses))
	return b.Bytes()
}

// Sign returns the base64 (raw std) Ed25519 signature of the record.
func Sign(a domain.Approval, priv ed25519.PrivateKey, rootID string) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("exec approval signing key is %d bytes, want %d", len(priv), ed25519.PrivateKeySize)
	}
	return base64.RawStdEncoding.EncodeToString(ed25519.Sign(priv, SigningMessage(a, rootID))), nil
}

// SignatureValid verifies an approved record against the operator public key.
// Missing/malformed signatures, wrong keys, and records bound to another root
// all deny (fail closed).
func SignatureValid(a domain.Approval, pub ed25519.PublicKey, rootID string) bool {
	if a.Signature == "" || len(pub) != ed25519.PublicKeySize {
		return false
	}
	raw, err := base64.RawStdEncoding.DecodeString(a.Signature)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, SigningMessage(a, rootID), raw)
}
