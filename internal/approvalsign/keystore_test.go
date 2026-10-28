package approvalsign

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixedPass(create bool) ([]byte, error) { return []byte("correct-horse-battery"), nil }

func TestSigningKeyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kern", "exec-approval-signing.json")
	a := signTestApproval()
	sig, err := SignApproval(path, a, "/repo", fixedPass)
	if err != nil {
		t.Fatal(err)
	}
	a.Signature = sig
	pub, err := PublicKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if !SignatureValid(a, pub, "/repo") {
		t.Fatal("signature from the encrypted key must verify with the public key")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("signing key file mode = %o, want 600", info.Mode().Perm())
	}
	// The public key file must be readable without a passphrase (verification
	// runs at exec time, where no human is present).
	if _, err := PublicKey(path); err != nil {
		t.Fatalf("PublicKey must not require the passphrase: %v", err)
	}
}

func TestSigningKeyWrongPassphraseFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.json")
	a := signTestApproval()
	if _, err := SignApproval(path, a, "/repo", fixedPass); err != nil {
		t.Fatal(err)
	}
	bad := func(create bool) ([]byte, error) { return []byte("wrong-passphrase-value"), nil }
	if _, err := SignApproval(path, a, "/repo", bad); err == nil {
		t.Fatal("signing with the wrong passphrase must fail")
	}
}

func TestSigningKeyShortPassphraseRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.json")
	short := func(create bool) ([]byte, error) { return []byte("short"), nil }
	if _, err := SignApproval(path, signTestApproval(), "/repo", short); err == nil {
		t.Fatal("a passphrase shorter than the minimum must be refused on creation")
	}
}

func TestSigningKeyDowngradeRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "signing.json")
	if _, err := SignApproval(path, signTestApproval(), "/repo", fixedPass); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Force the KDF work factor below the floor; the loader must refuse it.
	data = []byte(strings.Replace(string(data), `"iter": 600000`, `"iter": 1000`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PublicKey(path); err == nil {
		t.Fatal("a downgraded KDF work factor must be rejected")
	}
}
