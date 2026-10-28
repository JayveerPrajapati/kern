package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGlobalPreCommitHookBlocksCommitOnVerifyFailure pins the hook half of
// P0-1 (2026-10-28 adversarial audit): the installed machine-wide pre-commit
// hook propagates a non-zero `kern verify` status, so a real `git commit` is
// BLOCKED when the security gate fails. The companion verifycmd test
// TestRunVerifyStagedSecurityCriticalSecretExitsFail proves a staged private
// key produces exactly that non-zero status; this test proves the hook acts
// on it. Together they close the "critical secret commits silently" finding.
func TestGlobalPreCommitHookBlocksCommitOnVerifyFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	home := isolatedHome(t)

	hooks := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte(globalPreCommitHookScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Stand-in for the kern binary: the hook's contract is "non-zero status
	// blocks the commit", which is what a critical security finding now
	// produces (the verifycmd test pins that mapping).
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeKern := filepath.Join(bin, "kern")
	if err := os.WriteFile(fakeKern, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	const pem = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA0auditfixturebody\n-----END RSA PRIVATE KEY-----\n"
	if err := os.WriteFile(filepath.Join(repo, "leak.env"), []byte("PRIVATE_KEY="+pem), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "leak.env")

	cmd := exec.Command("git", "-c", "core.hooksPath="+hooks, "commit", "-m", "leak private key")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "KERN_BINARY="+fakeKern)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a failing verify must block the commit, but it succeeded:\n%s", out)
	}
	if !strings.Contains(string(out), "Commit blocked") {
		t.Fatalf("expected the hook's blocked message, got:\n%s", out)
	}
	// The commit must not have landed.
	logCmd := exec.Command("git", "rev-list", "--count", "HEAD")
	logCmd.Dir = repo
	if count, lerr := logCmd.CombinedOutput(); lerr == nil && strings.TrimSpace(string(count)) != "0" {
		t.Fatalf("commit landed despite the hook (HEAD count %s)", strings.TrimSpace(string(count)))
	}
}
