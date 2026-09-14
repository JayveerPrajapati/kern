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

func TestRenderCompactSecurityFindingDetails(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictPassWithWarning,
		Summary: "security: 1 finding",
		Security: &SecurityResult{
			OK:    true,
			Count: 1,
			Low:   1,
			Findings: []Finding{
				{
					File:     "auth/login.go",
					Line:     42,
					Severity: "info",
					Rule:     "ip-leak",
					Message:  "unencrypted IP reference",
				},
			},
		},
	}
	out := RenderCompact(v)
	want := "auth/login.go:42 [info] ip-leak: unencrypted IP reference"
	if !strings.Contains(out, want) {
		t.Errorf("RenderCompact missing finding detail %q in:\n%s", want, out)
	}
}

