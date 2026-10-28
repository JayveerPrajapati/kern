//go:build darwin

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSeatbeltWriteConfinementProfileShape pins the write arm: it must be
// emitted only when a write root is supplied, it must DENY file-write* before
// the allows (SBPL is last-match-wins), and the root plus the device sinks must
// be re-allowed.
func TestSeatbeltWriteConfinementProfileShape(t *testing.T) {
	root := "/Users/someone/project"
	prof := seatbeltProfileWithWriteRoot("/Users/someone", true, false, root)
	deny := strings.Index(prof, "(deny file-write*)")
	if deny < 0 {
		t.Fatalf("write-confined profile missing the deny arm:\n%s", prof)
	}
	allowRoot := strings.Index(prof, "(allow file-write* (subpath \""+root+"\"))")
	if allowRoot < 0 {
		t.Fatalf("write-confined profile missing the root allow:\n%s", prof)
	}
	if allowRoot < deny {
		t.Fatalf("the root allow must come AFTER the deny (last-match-wins):\n%s", prof)
	}
	for _, dev := range []string{"/dev/null", "/dev/stdout", "/dev/stderr"} {
		if !strings.Contains(prof, "(allow file-write* (literal \""+dev+"\"))") {
			t.Errorf("profile missing device sink %s", dev)
		}
	}

	// Empty root keeps the pre-confinement shape exactly.
	if got := seatbeltProfileWithWriteRoot("/Users/someone", true, false, ""); strings.Contains(got, "file-write") {
		t.Fatalf("no write root must not emit a write contract:\n%s", got)
	}
	// Confinement off drops the arm even when a root is supplied.
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "0")
	if got := seatbeltProfileWithWriteRoot("/Users/someone", fsConfinementEnabled(), false, root); strings.Contains(got, "file-write") {
		t.Fatalf("confinement-off must not emit a write contract:\n%s", got)
	}
}

// TestSeatbeltWriteConfinementLive is the live proof the 2026-10-26 audit
// asked for: with a write root set, an absolute-path write INSIDE the root
// succeeds and one OUTSIDE it is denied by Seatbelt itself.
func TestSeatbeltWriteConfinementLive(t *testing.T) {
	if os.Getenv("KERN_SANDBOX_ACTIVE") == "1" {
		t.Skip("cannot nest sandbox-exec inside an active kern sandbox")
	}
	bin, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec not available")
	}
	t.Setenv("KERN_SANDBOX_FS_CONFINEMENT", "")

	root := t.TempDir()
	// /tmp is deliberately NOT os.TempDir() on darwin (that is
	// /var/folders/…, which the contract allows) — so it is a true outside
	// target that the user CAN normally write.
	outside := filepath.Join("/tmp", "kern-write-confine-"+strconv.Itoa(os.Getpid())+".txt")
	_ = os.Remove(outside)
	defer func() { _ = os.Remove(outside) }()

	prof := seatbeltProfileWithWriteRoot("/Users/nobody", true, false, root)

	inside := filepath.Join(root, "ok.txt")
	if out, err := exec.Command(bin, "-p", prof, "sh", "-c", "echo hi > "+inside).CombinedOutput(); err != nil {
		t.Fatalf("write INSIDE the root must be allowed: %v (%s)", err, out)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatalf("inside write did not land: %v", err)
	}

	if err := exec.Command(bin, "-p", prof, "sh", "-c", "echo pwn > "+outside).Run(); err == nil {
		t.Fatalf("write OUTSIDE the root must be denied by the seatbelt profile")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("escaped write landed at %s — the write contract did not hold", outside)
	}
}
