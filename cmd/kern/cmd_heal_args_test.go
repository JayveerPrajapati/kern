package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// QA F3: local argument validation must precede the LLM-provider probe —
// a bad root or missing --file fails as a usage error (exit 2) without
// needing any provider.

func TestHealRootValidation(t *testing.T) {
	expectExit(t, 2, func() {
		runHeal([]string{"/nonexistent-heal-root"})
	})
}

func TestHealFileValidation(t *testing.T) {
	expectExit(t, 2, func() {
		runHeal([]string{"--file", "/nonexistent-fix-target.go"})
	})
}

// TestHealForceNonInteractiveRefuses pins the fail-closed gate: --force
// without --yes on a non-interactive stdin (scripts, CI, MCP) must refuse
// outright — never hang on a prompt, never silently proceed. The test forces
// os.Stdin to a regular file (not a char device) so it is deterministic
// regardless of how `go test` was launched.
func TestHealForceNonInteractiveRefuses(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "stdin"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()

	stderr, code := captureStderrExit(t, func() {
		runHeal([]string{"--force"})
	})
	if code != 1 {
		t.Fatalf("heal --force non-interactive exit = %d, want 1 (fail closed)", code)
	}
	if !strings.Contains(stderr, "refusing --force without --yes in non-interactive mode") {
		t.Fatalf("stderr missing the refusal message, got: %s", stderr)
	}
}

// TestHealForceYesSkipsGateProceedsToValidation pins that --yes skips the
// gate prompt entirely: with --force --yes the run proceeds past the gate to
// the next validation step (the nonexistent root fails as a usage error, exit
// 2 — NOT the gate refusal, exit 1).
func TestHealForceYesSkipsGateProceedsToValidation(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "stdin"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()

	expectExit(t, 2, func() {
		runHeal([]string{"--force", "--yes", "/nonexistent-heal-root"})
	})
}

// TestHealForceGateConfirmed exercises the gate prompt decision in isolation:
// exactly y/Y confirms; an empty line, EOF, or any other input declines.
func TestHealForceGateConfirmed(t *testing.T) {
	if !healForceGateConfirmed(strings.NewReader("y\n")) {
		t.Error("y must confirm the gate override")
	}
	if !healForceGateConfirmed(strings.NewReader("Y\n")) {
		t.Error("Y must confirm the gate override")
	}
	for _, in := range []string{"\n", "yes\n", "n\n", " ", "", "  \n"} {
		if healForceGateConfirmed(strings.NewReader(in)) {
			t.Errorf("input %q must decline the gate override", in)
		}
	}
}
