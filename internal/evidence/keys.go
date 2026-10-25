package evidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
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
		// The pub read can transiently fail on FIRST USE: another process
		// won the private-key write (O_EXCL, see below) and has not written
		// the pubkey yet — the winner's second write is immediate, so the
		// ErrNotExist case retries via keyPairFromFileRetry instead of
		// failing the whole load. Every other failure (unreadable pub,
		// corrupt or mismatched pair) still fails closed with the original
		// refusal semantics.
		pub, perr := os.ReadFile(pubPath)
		if perr != nil && !errors.Is(perr, fs.ErrNotExist) {
			return nil, fmt.Errorf("evidence: private key present but public key unreadable: %w", perr)
		}
		if perr == nil {
			kp, kerr := keyPairFrom(priv, pub)
			if kerr != nil {
				return nil, fmt.Errorf("evidence: corrupt project key (refusing to regenerate): %w", kerr)
			}
			return kp, nil
		}
		kp, kerr := keyPairFromFileRetry(privPath, pubPath)
		if kerr != nil {
			return nil, fmt.Errorf("evidence: private key present but public key unreadable: %w", kerr)
		}
		return kp, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
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
	// M8: two processes can race the first-use path. Both see ErrNotExist,
	// both generate DIFFERENT keypairs, and a bare os.WriteFile lets the
	// loser OVERWRITE the winner's private key while the pubkey files
	// interleave — leaving a mismatched pair on disk (the store is bricked:
	// every bundle the winner signed verifies against the wrong pubkey).
	// O_EXCL makes each write atomic: only the first writer of the private
	// key wins; the loser gets EEXIST, re-reads the winner's pair, and
	// validates it. A corrupt winner's key still fails closed (no silent
	// regeneration).
	privWritten := false
	if err := writeKeyExcl(privPath, priv, 0o600); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("evidence: write private key: %w", err)
		}
	} else {
		privWritten = true
		// We won the private-key race, so the public key must be written
		// too (it is not derivable on read — both files are stored). O_EXCL
		// again: if another process won the pub write (impossible when we
		// won the priv write — the pub writer is the same winner), the
		// re-read below validates whatever is on disk.
		if err := writeKeyExcl(pubPath, pub, 0o644); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("evidence: write public key: %w", err)
		}
	}
	// The public key is derivable from the private key, so the private key
	// write is the arbiter: the winner's private key decides the pair. If
	// the private key write lost the race, re-read BOTH from the winner and
	// validate; if the private key write won but the pubkey write loses
	// (another process wrote the SAME priv bytes, then raced the pub),
	// re-read and validate too. In every case the on-disk pair must match.
	// The loser may read in the gap between the winner's priv and pub
	// writes (priv present, pub not yet); retry briefly — the winner's
	// second write is immediate.
	kp, kerr := keyPairFromFileRetry(privPath, pubPath)
	if kerr != nil {
		if privWritten {
			// We won the private-key race but the on-disk pair is corrupt —
			// a concurrent process wrote a mismatched pubkey. Fail closed
			// (never regenerate over a corrupt key) with a repair hint.
			return nil, fmt.Errorf("evidence: first-use key race produced a mismatched pair: %w — remove %s and %s to regenerate", kerr, privPath, pubPath)
		}
		return nil, fmt.Errorf("evidence: project key: %w", kerr)
	}
	return kp, nil
}

// keyPairFromFileRetry reads the stored pair, retrying briefly when the
// public key is not yet present (a first-use racer reading between the
// winner's private and public writes). The winner writes both files
// immediately, so a short bounded retry closes the gap.
func keyPairFromFileRetry(privPath, pubPath string) (*KeyPair, error) {
	var lastErr error
	for i := 0; i < 10; i++ {
		kp, err := keyPairFromFile(privPath, pubPath)
		if err == nil {
			return kp, nil
		}
		lastErr = err
		if !errors.Is(err, fs.ErrNotExist) {
			// A real corruption/mismatch will not self-heal; fail fast.
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil, lastErr
}

// writeKeyExcl writes data to path with O_CREATE|O_EXCL, so only the first
// writer succeeds. A present file is reported as fs.ErrExist.
func writeKeyExcl(path string, data []byte, mode fs.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

// keyPairFromFile loads and validates the stored keypair. On a first-use
// race the winner's bytes are re-read (the loser never sees its own
// discarded pair); a mismatched or corrupt pair fails closed.
func keyPairFromFile(privPath, pubPath string) (*KeyPair, error) {
	priv, err := os.ReadFile(privPath)
	if err != nil {
		return nil, err
	}
	pub, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, err
	}
	return keyPairFrom(priv, pub)
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
