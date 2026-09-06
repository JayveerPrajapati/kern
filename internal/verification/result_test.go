package verification

import (
	"strings"
	"testing"
	"time"
)

// TestRenderCompactSurfacesFailVerdict (report A11): a FAIL verdict must be
// rendered as a typed verdict plus per-check status, never collapsed into a
// bare error.
func TestRenderCompactSurfacesFailVerdict(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictFail,
		Summary: "security failed: 1 critical finding",
		Build:   &BuildResult{OK: true, Duration: 141 * time.Millisecond},
		Security: &SecurityResult{
			OK:       false,
			Count:    3,
			Critical: 1,
			High:     2,
			Low:      0,
		},
		Architecture: &ArchitectureResult{OK: true},
	}
	out := RenderCompact(v)
	for _, want := range []string{
		"verdict: FAIL",
		"summary: security failed: 1 critical finding",
		"build: OK (141ms)",
		"security: FAIL findings=3 critical=1 high=2 low=0",
		"architecture: OK violations=0 warnings=0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderCompact missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "verdict: PASS") {
		t.Errorf("RenderCompact must not fabricate a PASS verdict:\n%s", out)
	}
}