package approvalsign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// KeyInfo describes one operator key file for `kern approval key status`.
type KeyInfo struct {
	Path        string
	Exists      bool
	Mode        fs.FileMode
	Size        int64
	Modified    time.Time
	Fingerprint string // short fingerprint of the public half; "" when absent
	Problem     string // why the file is unusable, if it is
}

// Fingerprint is the operator-facing identifier of a signing key: the first 8
// bytes of SHA-256 over the public key, hex. It names a key without exposing
// any private material and is stable across machines and restarts.
func Fingerprint(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Inspect reports the on-disk state of a signing-key envelope. It never
// prompts: the public half is stored in the clear, so status works
// non-interactively (and from an agent).
func Inspect(path string) KeyInfo {
	info := KeyInfo{Path: path}
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			info.Problem = "absent (created on first use)"
		} else {
			info.Problem = err.Error()
		}
		return info
	}
	info.Exists = true
	info.Mode = fi.Mode().Perm()
	info.Size = fi.Size()
	info.Modified = fi.ModTime()
	pub, perr := PublicKey(path)
	if perr != nil {
		info.Problem = perr.Error()
		return info
	}
	info.Fingerprint = Fingerprint(pub)
	return info
}

// PermWarning reports a group/other permission leak on a key file, or "" when
// the mode is private or the file is absent.
func (i KeyInfo) PermWarning() string {
	if !i.Exists || i.Mode&0o077 == 0 {
		return ""
	}
	return fmt.Sprintf("%s is mode %04o (group/other readable) — chmod 600 it", i.Path, i.Mode)
}

// RotateFile moves an existing key file aside as <path>.bak-<UTC timestamp>
// and returns the backup path ("" when there was nothing to move). It NEVER
// deletes key material: a rotation is reversible, and the backup is what makes
// "which key signed this record" answerable after the fact.
func RotateFile(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(path, backup); err != nil {
		return "", fmt.Errorf("move %s aside: %w", path, err)
	}
	return backup, nil
}
