package evidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// KeyPair is a project-local ed25519 identity for evidence-bundle signing
// (C4 key identity). Keys live under <root>/.kern/keys/ — per-repo,
// gitignored with the rest of .kern — and provide tamper-evidence beyond
// the SHA-256 seal: a bundle signature cannot be forged by an attacker who
// only modifies the bundle bytes. The format leaves a clean seam for
// external PKI (sigstore / GitHub attestation): the SignatureSection carries
// an algorithm field, so a future "sigstore" signer slots in without schema
// change.
type KeyPair struct {
	PublicKey   ed25519.PublicKey
	PrivateKey  ed25519.PrivateKey
	Fingerprint string // hex(sha256(pubkey))[:16] — the human trust anchor
}

// KeysDir is the per-project key directory under root/.kern.
func KeysDir(root string) string {
	return filepath.Join(root, ".kern", "keys")
}

// LoadOrCreateKeys loads the project's ed25519 keypair, creating it on first
// use. The private key is written 0600 (owner-only); a present-but-corrupt
// key fails closed rather than being silently regenerated (a regenerated key
// would invalidate every bundle the old key signed).
func LoadOrCreateKeys(root string) (*KeyPair, error) {
	dir := KeysDir(root)
	privPath := filepath.Join(dir, "ed25519")
	pubPath := filepath.Join(dir, "ed25519.pub")

	priv, err := os.ReadFile(privPath)
	if err == nil {
		pub, perr := os.ReadFile(pubPath)
		if perr != nil {
			return nil, fmt.Errorf("evidence: private key present but public key unreadable: %w", perr)
		}
		kp, kerr := keyPairFrom(priv, pub)
		if kerr != nil {
			return nil, fmt.Errorf("evidence: corrupt project key (refusing to regenerate): %w", kerr)
		}
		return kp, nil
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("evidence: read project key: %w", err)
	}

	// First use: generate a fresh keypair.
	pub, priv, gerr := ed25519.GenerateKey(rand.Reader)
	if gerr != nil {
		return nil, fmt.Errorf("evidence: generate project key: %w", gerr)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("evidence: create key dir: %w", err)
	}
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		return nil, fmt.Errorf("evidence: write private key: %w", err)
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		return nil, fmt.Errorf("evidence: write public key: %w", err)
	}
	kp := &KeyPair{
		PublicKey:   pub,
		PrivateKey:  priv,
		Fingerprint: fingerprint(pub),
	}
	return kp, nil
}

// keyPairFrom rebuilds a KeyPair from stored key bytes, validating lengths.
func keyPairFrom(priv, pub []byte) (*KeyPair, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, errors.New("private key has the wrong length")
	}
	if len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("public key has the wrong length")
	}
	if !equalBytes(pub, priv[ed25519.PublicKeySize:]) {
		return nil, errors.New("public key does not match the private key")
	}
	return &KeyPair{
		PublicKey:   ed25519.PublicKey(pub),
		PrivateKey:  ed25519.PrivateKey(priv),
		Fingerprint: fingerprint(pub),
	}, nil
}

// Sign signs payload with the private key.
func (kp *KeyPair) Sign(payload []byte) []byte {
	return ed25519.Sign(kp.PrivateKey, payload)
}

// VerifySignature reports whether sig is a valid ed25519 signature over
// payload by the given public key.
func VerifySignature(pub ed25519.PublicKey, payload, sig []byte) bool {
	if len(pub) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, payload, sig)
}

// fingerprint returns the human trust anchor for a public key: the first 16
// hex chars of its SHA-256.
func fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:])[:16]
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
