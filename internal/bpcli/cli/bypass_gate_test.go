// Bypass-gate tests for audit-table-2 B: humanBypassDecision (the TTY-gated
// emergency-bypass decision) and checkBypassHistory (the doctor counter).
package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/governance"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// setBypassInputs swaps the test-injectable terminal probe and confirmation
// reader for the duration of the test and returns a restore func.
func setBypassInputs(t *testing.T, probe func() bool, read func(r io.Reader) (string, error)) {
	t.Helper()
	oldProbe := terminalProbe
	oldRead := readBypassConfirmation
	oldTimeout := bypassConfirmTimeout
	terminalProbe = probe
	readBypassConfirmation = read
	bypassConfirmTimeout = time.Second
	t.Cleanup(func() {
		terminalProbe = oldProbe
		readBypassConfirmation = oldRead
		bypassConfirmTimeout = oldTimeout
	})
}

// TestHumanBypassDecision pins the audit-table-2 B gate: the env vars are
// honored ONLY when a human confirms at an interactive terminal; otherwise
// the bypass is refused with a mode the caller audits.
func TestHumanBypassDecision(t *testing.T) {
	t.Run("env unset stays inactive", func(t *testing.T) {
		t.Setenv("KERN_BYPASS", "")
		t.Setenv("KERN_ENFORCE", "")
		active, reason, mode := humanBypassDecision()
		if active || reason != "" || mode != "" {
			t.Fatalf("got (%v, %q, %q), want (false, \"\", \"\")", active, reason, mode)
		}
	})

	t.Run("no terminal refuses no-terminal", func(t *testing.T) {
		t.Setenv("KERN_BYPASS", "1")
		setBypassInputs(t, func() bool { return false }, func(r io.Reader) (string, error) {
			return "YES", nil
		})
		active, reason, mode := humanBypassDecision()
		if active || mode != "no-terminal" {
			t.Fatalf("got (%v, %q, %q), want (false, reason, no-terminal)", active, reason, mode)
		}
	})

	t.Run("terminal YES honors", func(t *testing.T) {
		t.Setenv("KERN_BYPASS", "1")
		t.Setenv("KERN_BYPASS_REASON", "P0 hotfix")
		setBypassInputs(t, func() bool { return true }, func(r io.Reader) (string, error) {
			return "YES\n", nil
		})
		active, reason, mode := humanBypassDecision()
		if !active || mode != "" {
			t.Fatalf("got (%v, %q, %q), want (true, reason, \"\")", active, reason, mode)
		}
		if reason != "P0 hotfix" {
			t.Fatalf("reason = %q, want the custom KERN_BYPASS_REASON", reason)
		}
	})

	t.Run("terminal non-yes declines", func(t *testing.T) {
		t.Setenv("KERN_BYPASS", "1")
		setBypassInputs(t, func() bool { return true }, func(r io.Reader) (string, error) {
			return "no\n", nil
		})
		active, _, mode := humanBypassDecision()
		if active || mode != "declined" {
			t.Fatalf("got (%v, %q), want (false, declined)", active, mode)
		}
	})

	t.Run("terminal timeout refuses", func(t *testing.T) {
		t.Setenv("KERN_ENFORCE", "0")
		setBypassInputs(t, func() bool { return true }, func(r io.Reader) (string, error) {
			return "", errors.New("bypass confirmation timed out")
		})
		active, _, mode := humanBypassDecision()
		if active || mode != "timeout" {
			t.Fatalf("got (%v, %q), want (false, timeout)", active, mode)
		}
	})
}

// TestCheckBypassHistory pins the doctor counter: honored vs refused bypasses
// are counted from the machine-wide guard trail and the project chain, and a
// non-zero honored count is a WARN (never an error).
func TestCheckBypassHistory(t *testing.T) {
	t.Run("no bypasses is OK", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		root := t.TempDir()
		c := checkBypassHistory(root)
		if c.Status != statusOK {
			t.Fatalf("status = %s, want ok (%s)", c.Status, c.Detail)
		}
		if !strings.Contains(c.Detail, "0 bypasses honored") {
			t.Fatalf("detail should say 0 honored: %s", c.Detail)
		}
	})

	t.Run("honored bypasses WARN", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		root := t.TempDir()
		// Seed the machine-wide guard trail with one honored + one refused.
		trail := filepath.Join(home, ".kern", "audit", "bypass.jsonl")
		if err := os.MkdirAll(filepath.Dir(trail), 0o755); err != nil {
			t.Fatal(err)
		}
		honored := `{"ts":"2026-10-05T00:00:00Z","event":"kern-guard-bypass","tool":"read","mode":"confirmed","pwd":"/x"}`
		refused := `{"ts":"2026-10-05T00:01:00Z","event":"kern-guard-bypass","tool":"bash","mode":"refused:no-tty","pwd":"/x"}`
		if err := os.WriteFile(trail, []byte(honored+"\n"+refused+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Seed the project chain with a bypassed entry via the real writer.
		recordBypassAudit(root, "P0 hotfix", []string{"architecture"}, 1, "bypassed")

		c := checkBypassHistory(root)
		if c.Status != statusWarn {
			t.Fatalf("status = %s, want warn (%s)", c.Status, c.Detail)
		}
		if !strings.Contains(c.Detail, "2 bypasses honored") {
			t.Fatalf("detail should count 2 honored (1 guard + 1 chain): %s", c.Detail)
		}
		if !strings.Contains(c.Detail, "1 refused") {
			t.Fatalf("detail should count 1 refused: %s", c.Detail)
		}
	})

	t.Run("refusals only are OK", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		root := t.TempDir()
		trail := filepath.Join(home, ".kern", "audit", "bypass.jsonl")
		if err := os.MkdirAll(filepath.Dir(trail), 0o755); err != nil {
			t.Fatal(err)
		}
		refused := `{"ts":"2026-10-05T00:01:00Z","event":"kern-guard-bypass","tool":"bash","mode":"refused:no-tty","pwd":"/x"}`
		if err := os.WriteFile(trail, []byte(refused+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		c := checkBypassHistory(root)
		if c.Status != statusOK {
			t.Fatalf("status = %s, want ok (refusals only are not a warn): %s", c.Status, c.Detail)
		}
		if !strings.Contains(c.Detail, "0 bypasses honored, 1 refused") {
			t.Fatalf("detail should count 0 honored 1 refused: %s", c.Detail)
		}
	})
}

// TestRecordBypassAuditRefusalResult pins that a refused bypass is written to
// the governance chain with Result refused:<mode> so `kern audit` and doctor
// can distinguish attempts from honored bypasses.
func TestRecordBypassAuditRefusalResult(t *testing.T) {
	root := t.TempDir()
	recordBypassAudit(root, "agent tried to bypass", []string{"architecture"}, 1, "refused:no-terminal")

	auditDir := filepath.Join(root, ".kern", "audit")
	l := governance.NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
	if _, err := l.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	found := false
	for i := range l.All() {
		e := l.All()[i]
		if e.Action == "bypass" && strings.HasPrefix(e.Result, "refused:") {
			found = true
			if e.Policy != "emergency-bypass" {
				t.Errorf("policy = %q, want emergency-bypass", e.Policy)
			}
		}
	}
	if !found {
		t.Fatalf("no refused bypass entry in chain: %+v", l.All())
	}
}
