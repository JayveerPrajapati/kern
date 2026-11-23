package exec

import (
	"context"
	"strings"
	"testing"
)

// TestExecStderrMarkerOnSuccess (audit F5, 2026-11-21): when a script exits 0
// with stderr output, the kern_exec response must append it under an explicit
// marker after stdout (PII-masked like the rest). A quiet script must not see
// the marker.
func TestExecStderrMarkerOnSuccess(t *testing.T) {
	t.Setenv("KERN_EXEC_POSTURE", "ask")
	out, err := Exec(context.Background(), map[string]any{
		"code": "echo out-ok; echo err-visibility-test >&2",
		"lang": "bash",
	})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if !strings.Contains(out, "out-ok") {
		t.Fatalf("stdout missing: %q", out)
	}
	i := strings.Index(out, "--- stderr ---")
	if i < 0 {
		t.Fatalf("stderr marker missing from response:\n%s", out)
	}
	if !strings.Contains(out[i:], "err-visibility-test") {
		t.Fatalf("stderr output missing after marker:\n%s", out)
	}
	if strings.Index(out, "err-visibility-test") < i {
		t.Fatalf("stderr must come after stdout, got:\n%s", out)
	}

	quiet, err := Exec(context.Background(), map[string]any{"code": "echo only-out", "lang": "bash"})
	if err != nil {
		t.Fatalf("quiet exec failed: %v", err)
	}
	if strings.Contains(quiet, "--- stderr ---") {
		t.Fatalf("quiet script must not carry the stderr marker:\n%s", quiet)
	}
}
