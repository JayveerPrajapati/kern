package approvalsign

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// SigningKeyVersion is the on-disk envelope version.
const SigningKeyVersion = 1

// MinPassphraseLen is the minimum operator passphrase accepted when creating
// the signing key. The private key is encrypted at rest; a short passphrase
// would make an offline brute force of the (readable) ciphertext cheap.
const MinPassphraseLen = 12

// signingKDFIterations is the PBKDF2-HMAC-SHA256 work factor. Reading also
// rejects a file that lowers this below signingKDFFloor (downgrade guard).
const (
	signingKDFIterations = 600000
	signingKDFFloor      = 100000
)

// PassphraseFunc returns the operator passphrase. create is true on first use
// (the caller should ask the operator to set and confirm a passphrase); false
// on later uses (ask once). The returned bytes are zeroed by this package
// after use.
type PassphraseFunc func(create bool) ([]byte, error)

// signingKeyFile is the on-disk envelope: the Ed25519 public key in the clear
// (needed for non-interactive verification) plus the seed encrypted with
// AES-256-GCM under a PBKDF2-derived key.
type signingKeyFile struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Iter       int    `json:"iter"`
	Salt       string `json:"salt"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	Public     string `json:"public"`
}

// PublicKey loads the operator's Ed25519 public key from path. It needs no
// passphrase: verification runs at exec time, where no human is present.
func PublicKey(path string) (ed25519.PublicKey, error) {
	kf, err := readKeyFile(path)
	if err != nil {
		return nil, err
	}
	pub, err := base64.RawStdEncoding.DecodeString(kf.Public)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("exec approval signing key %s: malformed public key", path)
	}
	return ed25519.PublicKey(pub), nil
}

// SignApproval signs one approval with the operator key at path, creating the
// key on first use. The private seed never leaves this process unencrypted.
func SignApproval(path string, a domain.Approval, rootID string, pass PassphraseFunc) (string, error) {
	kf, create, err := loadOrInitKeyFile(path)
	if err != nil {
		return "", err
	}
	passphrase, err := pass(create)
	if err != nil {
		return "", err
	}
	defer zero(passphrase)
	if create {
		if len(passphrase) < MinPassphraseLen {
			return "", fmt.Errorf("exec approval signing passphrase must be at least %d characters", MinPassphraseLen)
		}
		if err := initKeyFile(path, passphrase, kf); err != nil {
			return "", err
		}
		if kf, err = readKeyFile(path); err != nil {
			return "", err
		}
	}
	seed, err := decryptSeed(kf, passphrase)
	if err != nil {
		return "", fmt.Errorf("exec approval signing key %s: %w", path, err)
	}
	defer zero(seed)
	return Sign(a, ed25519.NewKeyFromSeed(seed), rootID)
}

// EnsureKey creates the signing key on first use (prompting via pass) and
// returns its public key. Kept separate so an operator can provision the key
// before the first approval.
func EnsureKey(path string, pass PassphraseFunc) (ed25519.PublicKey, error) {
	kf, create, err := loadOrInitKeyFile(path)
	if err != nil {
		return nil, err
	}
	if create {
		passphrase, perr := pass(true)
		if perr != nil {
			return nil, perr
		}
		defer zero(passphrase)
		if len(passphrase) < MinPassphraseLen {
			return nil, fmt.Errorf("exec approval signing passphrase must be at least %d characters", MinPassphraseLen)
		}
		if err := initKeyFile(path, passphrase, kf); err != nil {
			return nil, err
		}
	}
	return PublicKey(path)
}

// loadOrInitKeyFile reads an existing key envelope, or returns an empty
// envelope with create=true. Creation itself happens in initKeyFile so the
// caller can supply the passphrase exactly once.
func loadOrInitKeyFile(path string) (signingKeyFile, bool, error) {
	kf, err := readKeyFile(path)
	if err == nil {
		return kf, false, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return signingKeyFile{Version: SigningKeyVersion, KDF: "pbkdf2-sha256", Iter: signingKDFIterations}, true, nil
	}
	return signingKeyFile{}, false, err
}

func readKeyFile(path string) (signingKeyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return signingKeyFile{}, err
	}
	var kf signingKeyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		return signingKeyFile{}, fmt.Errorf("exec approval signing key %s: corrupt envelope: %w", path, err)
	}
	if kf.Version != SigningKeyVersion || kf.KDF != "pbkdf2-sha256" {
		return signingKeyFile{}, fmt.Errorf("exec approval signing key %s: unsupported version/kdf", path)
	}
	if kf.Iter < signingKDFFloor {
		return signingKeyFile{}, fmt.Errorf("exec approval signing key %s: KDF work factor %d is below the %d floor (refusing a downgraded key)", path, kf.Iter, signingKDFFloor)
	}
	for name, v := range map[string]string{"salt": kf.Salt, "nonce": kf.Nonce, "ciphertext": kf.Ciphertext, "public": kf.Public} {
		if v == "" {
			return signingKeyFile{}, fmt.Errorf("exec approval signing key %s: missing %s", path, name)
		}
	}
	return kf, nil
}

// initKeyFile generates a fresh Ed25519 seed, encrypts it, and writes the
// envelope with O_CREATE|O_EXCL so two processes racing the first approval
// cannot silently overwrite each other's key.
func initKeyFile(path string, passphrase []byte, kf signingKeyFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return err
	}
	defer zero(seed)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	key, err := pbkdf2.Key(sha256.New, string(passphrase), salt, kf.Iter, 32)
	if err != nil {
		return err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	ct := gcm.Seal(nil, nonce, seed, nil)
	pub := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	kf = signingKeyFile{
		Version:    SigningKeyVersion,
		KDF:        "pbkdf2-sha256",
		Iter:       kf.Iter,
		Salt:       base64.RawStdEncoding.EncodeToString(salt),
		Nonce:      base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ct),
		Public:     base64.RawStdEncoding.EncodeToString(pub),
	}
	data, err := json.MarshalIndent(kf, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create exec approval signing key %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func decryptSeed(kf signingKeyFile, passphrase []byte) ([]byte, error) {
	salt, err := base64.RawStdEncoding.DecodeString(kf.Salt)
	if err != nil {
		return nil, errors.New("malformed salt")
	}
	nonce, err := base64.RawStdEncoding.DecodeString(kf.Nonce)
	if err != nil || len(nonce) != 12 {
		return nil, errors.New("malformed nonce")
	}
	ct, err := base64.RawStdEncoding.DecodeString(kf.Ciphertext)
	if err != nil {
		return nil, errors.New("malformed ciphertext")
	}
	key, err := pbkdf2.Key(sha256.New, string(passphrase), salt, kf.Iter, 32)
	if err != nil {
		return nil, err
	}
	defer zero(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	seed, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("wrong passphrase (or corrupt key file)")
	}
	if len(seed) != ed25519.SeedSize {
		return nil, errors.New("decrypted seed has the wrong length")
	}
	return seed, nil
}

func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
