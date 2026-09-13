package main

import (
	"strings"
	"testing"
)

// TestProseBareCallUsage (F-011): `kern prose` with no arguments must fail
// with a friendly usage message that documents the required <words> positional
// (maps prose words to symbol candidates) and shows an example — not a cryptic
// bare usage line.
func TestProseBareCallUsage(t *testing.T) {
	code := runProseExit(t, nil)
	if code != 2 {
		t.Fatalf("bare `kern prose` exit code = %d, want 2 (usage error)", code)
	}
}

// TestProseBareCallUsageMessage checks the stderr text of the bare call.
func TestProseBareCallUsageMessage(t *testing.T) {
	stderr := captureStderr(t, func() {
		_ = runProseExit(t, nil)
	})
	for _, want := range []string{
		"prose: missing <words> argument",
		"<words> is a natural-language phrase",
		"symbol candidates",
		"example",
		"kern prose \"save user\"",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("prose usage message missing %q; got:\n%s", want, stderr)
		}
	}
}

// runProseExit recovers the fatalUsage sentinel from runProse.
func runProseExit(t *testing.T, rest []string) (code int) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(exitError); ok {
				code = e.code
				return
			}
			panic(r)
		}
	}()
	return runProse(rest)
}
