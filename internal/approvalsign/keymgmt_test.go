package approvalsign

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectAbsentAndPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exec-approval-signing.json")

	absent := Inspect(path)
	if absent.Exists || absent.Problem == "" {
		t.Fatalf("absent key must report Exists=false with a Problem, got %+v", absent)
	}

	pub, err := EnsureKey(path, func(bool) ([]byte, error) { return []byte("test-passphrase-1234"), nil })
	if err != nil {
		t.Fatalf("EnsureKey: %v", err)
	}
	present := Inspect(path)
	if !present.Exists || present.Fingerprint != Fingerprint(pub) {
		t.Fatalf("present key must carry the public fingerprint, got %+v", present)
	}
	if present.Mode.Perm()&0o077 != 0 {
		t.Errorf("key mode %04o is not private", present.Mode)
	}
	if w := present.PermWarning(); w != "" {
		t.Errorf("private key must not warn: %s", w)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if w := Inspect(path).PermWarning(); w == "" {
		t.Error("group-readable key must warn")
	}
}

func TestFingerprintIsStableAndKeyDerived(t *testing.T) {
	seedA := make([]byte, ed25519.SeedSize)
	seedB := make([]byte, ed25519.SeedSize)
	seedB[0] = 1
	a := ed25519.NewKeyFromSeed(seedA).Public().(ed25519.PublicKey)
	b := ed25519.NewKeyFromSeed(seedB).Public().(ed25519.PublicKey)
	if Fingerprint(a) != Fingerprint(a) {
		t.Error("fingerprint is not stable")
	}
	if Fingerprint(a) == Fingerprint(b) {
		t.Error("different keys share a fingerprint")
	}
	if len(Fingerprint(a)) != 16 {
		t.Errorf("fingerprint length = %d, want 16 hex chars", len(Fingerprint(a)))
	}
}

// TestRotateFileKeepsMaterial is the never-delete contract: rotation must
// leave the old key on disk as a .bak file, and be a no-op when nothing is
// there.
func TestRotateFileKeepsMaterial(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exec-approval.key")
	if backup, err := RotateFile(path); err != nil || backup != "" {
		t.Fatalf("absent key: backup=%q err=%v, want a silent no-op", backup, err)
	}
	if err := os.WriteFile(path, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	backup, err := RotateFile(path)
	if err != nil {
		t.Fatalf("RotateFile: %v", err)
	}
	if backup == "" {
		t.Fatal("rotation of an existing key must return a backup path")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("original path still exists after rotation")
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup unreadable: %v", err)
	}
	if string(data) != "0123456789abcdef0123456789abcdef" {
		t.Errorf("backup content changed: %q", data)
	}
	if fi, err := os.Stat(backup); err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("backup mode is not private: %v %v", fi, err)
	}
}
