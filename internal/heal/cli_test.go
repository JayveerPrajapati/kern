package heal

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/incident"
)

// cliCaptureStderrExit runs fn (the exported RunHeal entry point returning
// the process exit code) with os.Stderr redirected to a pipe and returns the
// captured stderr together with the exit code.
func cliCaptureStderrExit(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = old }()
	code := fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

// QA F3: local argument validation must precede the LLM-provider probe —
// a bad root or missing --file fails as a usage error (exit 2) without
// needing any provider.

func TestHealRootValidation(t *testing.T) {
	if code := RunHeal([]string{"/nonexistent-heal-root"}); code != 2 {
		t.Fatalf("RunHeal(nonexistent root) exit = %d, want 2 (usage error)", code)
	}
}

func TestHealFileValidation(t *testing.T) {
	if code := RunHeal([]string{"--file", "/nonexistent-fix-target.go"}); code != 2 {
		t.Fatalf("RunHeal(--file nonexistent) exit = %d, want 2 (usage error)", code)
	}
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

	stderr, code := cliCaptureStderrExit(t, func() int {
		return RunHeal([]string{"--force"})
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

	if code := RunHeal([]string{"--force", "--yes", "/nonexistent-heal-root"}); code != 2 {
		t.Fatalf("RunHeal(--force --yes, nonexistent root) exit = %d, want 2 (usage error)", code)
	}
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

// TestHealPlaybookStoreLookupDriftIsMiss is the CLI twin of the MCP adapter
// drift test (internal/mcp/exec/heal_playbook_test.go): the recorded OLD
// content gates the replay — identical current content is a hit, drift is a
// MISS that escalates to the full LLM loop (deep-dive C4 poison-scope
// limiter).
func TestHealPlaybookStoreLookupDriftIsMiss(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := EncodeReplacements(root, []Replacement{
		{Path: "app.go", Content: "package main\n\nfunc main() {}\n"},
	})
	store := incident.NewPlaybookStore(root)
	if store == nil {
		t.Fatal("NewPlaybookStore returned nil")
	}
	if err := store.UpsertBySignature("sig12345", steps); err != nil {
		t.Fatal(err)
	}
	pb := &cliPlaybookStore{root: root, store: store}

	if reps, ok := pb.Lookup("sig12345"); !ok || len(reps) != 1 {
		t.Fatalf("identical content must be a hit, ok=%v reps=%v", ok, reps)
	}

	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package main\n\n// drifted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reps, ok := pb.Lookup("sig12345"); ok {
		t.Fatalf("drifted content must be a miss, got reps=%v", reps)
	}
}
