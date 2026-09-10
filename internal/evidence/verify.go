package evidence

import "fmt"

// TrustAnchor describes how a bundle's signature key identity was anchored to
// a trust root during verification.
type TrustAnchor string

const (
	// TrustAnchorAnchored means the bundle's key fingerprint was matched
	// against a caller-supplied expected fingerprint (the human trust anchor
	// a reviewer checked out-of-band).
	TrustAnchorAnchored TrustAnchor = "anchored"
	// TrustAnchorSelfAttested means verification relied solely on the
	// bundle-embedded public key because no expected fingerprint was supplied.
	// A self-consistent attacker can satisfy this check, so it must never be
	// presented as an anchored trust decision.
	TrustAnchorSelfAttested TrustAnchor = "self-attested"
)

// VerifyResult is the outcome of a bundle verification, including how the
// signature's key identity was anchored to a trust root. SelfAttested exists
// so verification OUTPUT can make the trust basis explicit: a "valid"
// signature verified against the bundle-embedded key only is NOT an anchored
// decision — an attacker can craft a self-consistent "signed" bundle.
type VerifyResult struct {
	// SelfAttested is true when the bundle was signed but no expected
	// fingerprint was supplied, so the key identity was verified against the
	// bundle-embedded public key only.
	SelfAttested bool
	// TrustAnchor is TrustAnchorAnchored (matched a supplied expected
	// fingerprint), TrustAnchorSelfAttested (embedded key only), or "" for
	// unsigned (digest-only) bundles with no key identity to anchor.
	TrustAnchor TrustAnchor
	// ExpectedFingerprint is the caller-supplied trust anchor when one was
	// provided; empty otherwise.
	ExpectedFingerprint string
}

// VerifyWithAnchor verifies the bundle's tamper-evidence seal (schema, SHA-256
// bundle hash, embedded-key ed25519 signature) and anchors the signature's key
// identity. When expectedFingerprint is non-empty, the bundle must be signed
// by exactly that key — the human trust anchor; an unsigned bundle or a
// fingerprint mismatch is an error, exactly as the CLI previously enforced.
// When expectedFingerprint is empty, a signed bundle is verified against the
// bundle-embedded key only and the result is marked SelfAttested, making the
// weaker trust basis explicit in the verification output.
func (b *Bundle) VerifyWithAnchor(expectedFingerprint string) (*VerifyResult, error) {
	if err := b.Verify(); err != nil {
		return nil, err
	}
	res := &VerifyResult{}
	if b.Signature == nil {
		if expectedFingerprint != "" {
			return nil, fmt.Errorf("evidence: bundle is unsigned, but an expected fingerprint %s was supplied", expectedFingerprint)
		}
		return res, nil // digest-only: no key identity to anchor
	}
	if expectedFingerprint == "" {
		res.SelfAttested = true
		res.TrustAnchor = TrustAnchorSelfAttested
		return res, nil
	}
	if b.Signature.KeyFingerprint != expectedFingerprint {
		return nil, fmt.Errorf("evidence: bundle key fingerprint %s does not match expected %s",
			b.Signature.KeyFingerprint, expectedFingerprint)
	}
	res.TrustAnchor = TrustAnchorAnchored
	res.ExpectedFingerprint = expectedFingerprint
	return res, nil
}
