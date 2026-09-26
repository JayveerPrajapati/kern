package script

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestScriptProfileIncludesFSBlocklist pins the script-side generator: the
// sensitive-path blocklist is present with the real $HOME substituted and
// the network base (allow default, deny network*) is preserved — the same
// BLOCKLIST-deny design as internal/sandbox.
func TestScriptProfileIncludesFSBlocklist(t *testing.T) {
	home := "/Users/scriptuser"
	t.Setenv("HOME", home)
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "")
	prof := seatbeltScriptProfile()
	base := "(version 1)\n(allow default)\n(deny network*)"
	if !strings.HasPrefix(prof, base) {
		t.Errorf("profile must start with the network base verbatim, got:\n%s", prof)
	}
	for _, p := range sensitivePathDirs {
		want := "(deny file-read-data (subpath \"" + filepath.Join(home, p) + "\"))"
		if !strings.Contains(prof, want) {
			t.Errorf("profile missing deny rule for %q; want %q in:\n%s", p, want, prof)
		}
	}
}

// TestScriptProfileDeniesBothPathAliases mirrors the sandbox R2 root-cause
// regression test: with HOME under a symlinked dir (t.TempDir under
// /var -> /private/var on macOS, or a symlinked operator home), the script
// profile must deny BOTH the as-written and canonical spellings of every
// blocklisted path — the darwin kernel canonicalizes the ACCESSED path but
// matches profile subpaths as written, so a one-sided deny intermittently
// lets the other alias through.
func TestScriptProfileDeniesBothPathAliases(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	// The blocklisted subpaths must exist under the alias home so the
	// canonical form resolves (mirrors a populated real home).
	for _, p := range sensitivePathDirs {
		if err := os.MkdirAll(filepath.Join(alias, p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", alias)
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "")
	prof := seatbeltScriptProfile()
	for _, p := range sensitivePathDirs {
		asWritten := filepath.Join(alias, p)
		canon, err := filepath.EvalSymlinks(asWritten)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", asWritten, err)
		}
		for _, want := range []string{asWritten, canon} {
			deny := "(deny file-read-data (subpath \"" + want + "\"))"
			if !strings.Contains(prof, deny) {
				t.Errorf("profile must deny %q (as-written=%q canon=%q) in:\n%s", want, asWritten, canon, prof)
			}
		}
	}
}

// TestScriptProfileConfinementDisabled asserts the escape hatch: with
// KERN_SANDBOX_FS_CONFINEMENT=0 the profile is exactly the pre-confinement
// network profile (zero behavior change for confinement-off runs).
func TestScriptProfileConfinementDisabled(t *testing.T) {
	t.Setenv("HOME", "/Users/scriptuser")
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "0")
	prof := seatbeltScriptProfile()
	if strings.Contains(prof, "file-read-data") {
		t.Errorf("KERN_SANDBOX_FS_CONFINEMENT=0 must drop the file denies, got:\n%s", prof)
	}
	want := "(version 1)\n(allow default)\n(deny network*)"
	if prof != want {
		t.Errorf("confinement-off profile must equal the legacy network profile:\n got %q\nwant %q", prof, want)
	}
}

// TestScriptReadConfinementBlocksSensitivePath is the kern_exec live proof:
// a script reading a blocklisted file through an ABSOLUTE path (the gap the
// HOME redirect leaves) is DENIED, while a benign script still runs.
func TestScriptReadConfinementBlocksSensitivePath(t *testing.T) {
	if goruntime.GOOS != "darwin" {
		t.Skip("Seatbelt file-read-data confinement is darwin-only (Stage 1)")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available; FS confinement cannot apply")
	}
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "")

	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const secret = "KERN-SCRIPT-SBX-SENTINEL-67890"
	sentinel := filepath.Join(sshDir, "id_rsa")
	if err := os.WriteFile(sentinel, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Blocked: absolute-path read of a blocklisted file must fail and the
	// secret must never appear in stdout or stderr.
	res := RunScript(Run{Lang: "bash", Code: "cat " + sentinel})
	if res.OK {
		t.Fatalf("script read of %s must be denied; stdout=%q", sentinel, res.Stdout)
	}
	if strings.Contains(res.Stdout+res.Stderr, secret) {
		t.Fatalf("sentinel secret leaked through the script sandbox: stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	// Control: the file is readable outside the sandbox (deny is the
	// sandbox's, not the file's).
	if data, err := os.ReadFile(sentinel); err != nil || !strings.Contains(string(data), secret) {
		t.Fatalf("control read failed (sentinel not readable?): %v", err)
	}

	// ~zero UX cost: a benign script still runs under FS confinement.
	ok := RunScript(Run{Lang: "bash", Code: "echo hello"})
	if ok.Err != nil || !strings.Contains(ok.Stdout, "hello") {
		t.Fatalf("benign script must succeed under FS confinement: %v stdout=%q", ok.Err, ok.Stdout)
	}
}
