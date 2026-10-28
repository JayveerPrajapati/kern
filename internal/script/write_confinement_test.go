package script

import (
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestScriptProfileConfinesWrites pins the write contract added after the
// 2026-10-26 audit: kern_exec's Seatbelt profile used (allow default), so an
// absolute-path write escaped the ephemeral run dir into the operator's repo.
// The profile must deny file-write* globally and re-allow only the run dir
// plus the device sinks.
func TestScriptProfileConfinesWrites(t *testing.T) {
	t.Setenv("HOME", "/Users/writer")
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "")
	root := "/private/var/folders/x/kern-exec-probe"
	prof := seatbeltScriptProfileFor(root)
	if !strings.Contains(prof, "(deny file-write*)") {
		t.Fatalf("profile must deny writes globally, got:\n%s", prof)
	}
	denyAt := strings.Index(prof, "(deny file-write*)")
	allowAt := strings.Index(prof, "(allow file-write* (subpath \""+root+"\"))")
	if allowAt < 0 {
		t.Fatalf("profile must re-allow the run dir %q, got:\n%s", root, prof)
	}
	if allowAt < denyAt {
		t.Fatalf("the run-dir allow must come AFTER the global deny (last matching rule wins), got:\n%s", prof)
	}
	for _, dev := range []string{"/dev/null", "/dev/stdout", "/dev/stderr"} {
		if !strings.Contains(prof, `(allow file-write* (literal "`+dev+`"))`) {
			t.Fatalf("profile must allow writes to %s, got:\n%s", dev, prof)
		}
	}
	// The no-root profile (availability probe) stays write-unrestricted so
	// the probe itself is unaffected; the run path always passes a root.
	if strings.Contains(seatbeltScriptProfile(), "(deny file-write*)") {
		t.Fatal("seatbeltScriptProfile() with no write root must keep the legacy profile")
	}
}

// TestScriptWriteConfinementBlocksOutsideWrite is the kern_exec live proof:
// writing to an absolute path OUTSIDE the ephemeral run dir is denied and no
// file appears, while writing inside the run dir still works.
func TestScriptWriteConfinementBlocksOutsideWrite(t *testing.T) {
	if goruntime.GOOS == "darwin" && os.Getenv("KERN_SANDBOX_ACTIVE") == "1" {
		t.Skip("cannot nest sandbox-exec inside an active kern sandbox on macOS; covered by direct runs")
	}
	if goruntime.GOOS != "darwin" {
		t.Skip("Seatbelt write confinement is darwin-only")
	}
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available; write confinement cannot apply")
	}
	if !runtimeInstalled("bash") {
		t.Skip("bash not installed")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "")

	outside := filepath.Join(t.TempDir(), "escaped.txt")
	res := RunScript(Run{Lang: "bash", Code: "echo pwned > " + outside})
	if res.OK {
		t.Fatalf("write outside the run dir must be denied; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("sandbox escaped: %s was created", outside)
	}

	inside := RunScript(Run{Lang: "bash", Code: "echo ok > inside.txt && cat inside.txt"})
	if inside.Err != nil || !strings.Contains(inside.Stdout, "ok") {
		t.Fatalf("write inside the run dir must succeed: err=%v stdout=%q stderr=%q", inside.Err, inside.Stdout, inside.Stderr)
	}
}
