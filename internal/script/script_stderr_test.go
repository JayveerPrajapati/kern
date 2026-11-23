package script

import (
	"strings"
	"testing"
)

// TestRunScriptStderrOnSuccess (audit F5, 2026-11-21): a script that exits 0
// but writes to stderr must surface that output — warnings were silently
// dropped before, making scripts un-debuggable. Empty stderr stays empty.
func TestRunScriptStderrOnSuccess(t *testing.T) {
	res := RunScript(Run{Lang: "bash", Code: "echo out-ok; echo err-visibility-test >&2"})
	if res.Err != nil {
		t.Fatalf("script failed: %v", res.Err)
	}
	if !strings.Contains(res.Stdout, "out-ok") {
		t.Fatalf("stdout missing: %q", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "err-visibility-test") {
		t.Fatalf("success-path stderr must be surfaced, got: %q", res.Stderr)
	}
	// Quiet script: no stderr, no marker.
	quiet := RunScript(Run{Lang: "bash", Code: "echo only-out"})
	if quiet.Err != nil {
		t.Fatalf("quiet script failed: %v", quiet.Err)
	}
	if quiet.Stderr != "" {
		t.Fatalf("quiet script must have empty stderr, got: %q", quiet.Stderr)
	}
	// Failure path keeps its richer contract (exit code + stderr in Err).
	fail := RunScript(Run{Lang: "bash", Code: "echo boom >&2; exit 3"})
	if fail.Err == nil || !strings.Contains(fail.Stderr, "boom") {
		t.Fatalf("failure stderr contract regressed: err=%v stderr=%q", fail.Err, fail.Stderr)
	}
}
