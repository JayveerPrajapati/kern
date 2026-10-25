package contextcli

import (
	"strings"
	"testing"
)

// TestGuardClassifyCLIShell pins the `kern guard classify` CLI shell over
// internal/guardrule: the engine verdicts (pinned exhaustively in
// internal/guardrule) reach the CLI output unchanged.
func TestGuardClassifyCLIShell(t *testing.T) {
	out := captureStdout(t, func() {
		code := RunGuardClassify([]string{"ls -la docs/ 2>/dev/null"})
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	for _, want := range []string{"verdict: allow", "hint:", "hash:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
