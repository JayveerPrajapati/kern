//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/sandbox/landlock"
)

// sbxTestEnv redirects the sandbox child's TMPDIR into the (already granted)
// workspace root and points HOME at a fresh dir under the REAL temp root —
// which the child does NOT have granted once TMPDIR is redirected. This keeps
// the Landlock allowlist honest: a fixture "home" under t.TempDir() would be
// readable via the write tier's os.TempDir() grant, defeating the test.
func sbxTestEnv(t *testing.T) (root, home string) {
	t.Helper()
	realTmp := os.TempDir() // capture BEFORE redirecting TMPDIR
	root = t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(root, "tmp"))
	t.Setenv("KERN_ALLOW_UNISOLATED", "")
	t.Setenv("KERN_ALLOW_NET", "")
	var err error
	home, err = os.MkdirTemp(realTmp, "kern-sbx-home-")
	if err != nil {
		t.Fatalf("cannot create test home under %s: %v", realTmp, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	return root, home
}

// TestLandlockBlocksSensitiveReads is the Linux live proof mirroring the
// darwin fs-confinement test (fs_confinement_test.go): with Landlock
// available, a sandboxed read of a file under a blocklisted dir (~/.ssh) is
// DENIED and the secret never appears in the output, while a workspace read
// and `go version` still succeed.
func TestLandlockBlocksSensitiveReads(t *testing.T) {
	if !landlock.LandlockAvailable(nil) {
		t.Skip("Landlock not available on this host")
	}
	if _, err := os.Stat("/usr/bin/unshare"); err != nil {
		t.Skip("unshare not available")
	}
	root, home := sbxTestEnv(t)

	// Sentinel secret in a real blocklisted dir (~/.ssh) under the test HOME.
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "KERN-SBX-SENTINEL-SECRET-12345"
	sentinel := filepath.Join(sshDir, "id_rsa")
	if err := os.WriteFile(sentinel, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Blocked: reading a file under a blocklisted dir must fail and the
	// secret must never appear in the run's output.
	res := runGuarded(context.Background(), root, "cat", []string{sentinel}, 30*time.Second, true)
	if res.ExitCode == 0 {
		t.Fatalf("sandboxed read of %s must be denied; output: %q", sentinel, res.Output)
	}
	if strings.Contains(res.Output, secret) {
		t.Fatalf("sentinel secret leaked through the sandbox: %q", res.Output)
	}
	if res.Network == nil || !res.Network.FSConfined {
		t.Fatalf("expected FSConfined=true, got %+v", res.Network)
	}

	// Control: the same read OUTSIDE the sandbox succeeds — proving the deny
	// is the sandbox's, not the file's.
	if data, err := os.ReadFile(sentinel); err != nil || !strings.Contains(string(data), secret) {
		t.Fatalf("control read failed (sentinel not readable?): %v", err)
	}

	// Workspace read still works (the project being operated on is always
	// granted read).
	okFile := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(okFile, []byte("fine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := runGuarded(context.Background(), root, "cat", []string{okFile}, 30*time.Second, true); res.ExitCode != 0 {
		t.Fatalf("workspace read failed: %v (out=%q)", res.Err, res.Output)
	}

	// ~zero UX cost: go version inside the sandbox must still succeed.
	if _, err := exec.LookPath("go"); err == nil {
		gres := runGuarded(context.Background(), root, "go", []string{"version"}, 60*time.Second, true)
		if gres.ExitCode != 0 {
			t.Fatalf("go version inside sandbox failed: %v (out=%q)", gres.Err, gres.Output)
		}
	}
}

// TestLandlockWriteConfinement proves the write tier: a write to $HOME (not
// in the write allowlist) is denied while a write inside the workspace
// succeeds.
func TestLandlockWriteConfinement(t *testing.T) {
	if !landlock.LandlockAvailable(nil) {
		t.Skip("Landlock not available on this host")
	}
	if _, err := os.Stat("/usr/bin/unshare"); err != nil {
		t.Skip("unshare not available")
	}
	root, home := sbxTestEnv(t)

	if res := runGuarded(context.Background(), root, "touch", []string{filepath.Join(home, "evil-marker")}, 30*time.Second, true); res.ExitCode == 0 {
		t.Fatalf("write to $HOME unexpectedly succeeded; output: %q", res.Output)
	}
	if res := runGuarded(context.Background(), root, "touch", []string{filepath.Join(root, "ok")}, 30*time.Second, true); res.ExitCode != 0 {
		t.Fatalf("workspace write failed: %v (out=%q)", res.Err, res.Output)
	}
}

// TestLandlockDegradeEnvDisablesFSConfinement: with KERN_SANDBOX_FS_CONFINEMENT=0
// the run is unconfined and FSConfined reports false.
func TestLandlockDegradeEnvDisablesFSConfinement(t *testing.T) {
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "0")
	root := t.TempDir()
	res := runGuarded(context.Background(), root, "true", nil, 30*time.Second, true)
	if res.Network == nil || res.Network.FSConfined {
		t.Fatalf("expected FSConfined=false with confinement disabled, got %+v", res.Network)
	}
}
