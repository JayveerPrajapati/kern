package main

import (
	"strings"
	"testing"
)

// TestFatalPathExits2WithoutStackOutput pins the single-error-path contract
// (CLI sweep): a usage error routed through fatalUsage exits with code 2,
// prints a "kern: " message on stderr, and never leaks a Go panic stack
// trace to the user — the exitError sentinel is recovered by main() exactly
// like this test's recover block, so the user sees the message, not a
// goroutine dump.
func TestFatalPathExits2WithoutStackOutput(t *testing.T) {
	// `kern meta` with no request is a documented fatalUsage path (exit 2).
	// Route it through the real dispatcher so the sentinel recovery path is
	// exercised end to end, same as main().
	stderr, code := captureStderrExit(t, func() {
		dispatchCommand("meta", nil)
	})
	if code != 2 {
		t.Fatalf("dispatchCommand(meta) exit code = %d, want 2", code)
	}
	if stderr == "" {
		t.Fatal("expected a message on stderr from the fatal path")
	}
	if !strings.Contains(stderr, "kern: ") {
		t.Fatalf("expected the kern: message prefix on stderr, got: %.200s", stderr)
	}
	// The exitError sentinel is recovered (like main() does) — the user must
	// never see a stack trace.
	for _, leak := range []string{"goroutine ", "panic:", "exitError", ".go:"} {
		if strings.Contains(stderr, leak) {
			t.Fatalf("stderr leaked stack output %q:\n%s", leak, stderr)
		}
	}
}
