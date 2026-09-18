package verification

import (
	"strings"
	"testing"
	"time"
)

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

// TestRenderCompactIncludesFailureOutput (F-4): a failed check whose Output
// carries the actionable reason (e.g. the sandbox fail-closed denial) must
// surface that text after the status line instead of a bare FAIL.
func TestRenderCompactIncludesFailureOutput(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictFail,
		UnitTests: &TestResult{
			OK:     false,
			Passed: 0,
			Failed: 0,
			Output: "network isolation not available on this platform (darwin); refusing to run unisolated (fail-closed)",
		},
	}
	out := RenderCompact(v)
	for _, want := range []string{
		"tests: FAIL passed=0 failed=0 skipped=0",
		"network isolation not available on this platform (darwin); refusing to run unisolated (fail-closed)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderCompact missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderCompactPassingTestsHideOutput (F-4): a passing check must not
// leak its Output into the compact render — the failure text is printed only
// when the check failed.
func TestRenderCompactPassingTestsHideOutput(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictPass,
		UnitTests: &TestResult{
			OK:       true,
			Passed:   3,
			Failed:   0,
			Skipped:  1,
			Duration: 250 * time.Millisecond,
			Output:   "--- PASS: TestOne\n--- PASS: TestTwo\nok  example.com/m 0.250s",
		},
	}
	out := RenderCompact(v)
	if !strings.Contains(out, "tests: OK passed=3 failed=0 skipped=1") {
		t.Errorf("RenderCompact missing passing tests status line in:\n%s", out)
	}
	for _, leaked := range []string{"TestOne", "TestTwo", "ok  example.com/m"} {
		if strings.Contains(out, leaked) {
			t.Errorf("RenderCompact leaked passing test output %q in:\n%s", leaked, out)
		}
	}
}

// TestRenderCompactE2EFailureOutput (F-4): E2E results carry an Output field
// and must surface it on failure just like unit tests.
func TestRenderCompactE2EFailureOutput(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictFail,
		E2ETests: &E2ETestResult{
			OK:     false,
			Passed: 0,
			Failed: 0,
			Output: "e2e runner could not start: sandbox denied (fail-closed)",
		},
	}
	out := RenderCompact(v)
	for _, want := range []string{
		"e2e: FAIL passed=0 failed=0 skipped=0",
		"e2e runner could not start: sandbox denied (fail-closed)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderCompact missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderCompactIntegrationFailureOutput (F-4): Integration reuses
// TestResult, which carries Output; it must surface it on failure as well.
func TestRenderCompactIntegrationFailureOutput(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictFail,
		Integration: &TestResult{
			OK:     false,
			Passed: 0,
			Failed: 0,
			Output: "integration sandbox unavailable on darwin (fail-closed)",
		},
	}
	out := RenderCompact(v)
	for _, want := range []string{
		"integration: FAIL passed=0 failed=0 skipped=0",
		"integration sandbox unavailable on darwin (fail-closed)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RenderCompact missing %q in:\n%s", want, out)
		}
	}
}

// TestRenderCompactDependencySkipped: a skipped dependency check renders an
// explicit SKIPPED line (with the reason), never a PASS/FAIL status.
func TestRenderCompactDependencySkipped(t *testing.T) {
	v := VerificationResult{
		Verdict: "PASS",
		Summary: "build: PASS",
		Dependency: &DependencyResult{
			OK:      true,
			Skipped: "no supported dependency manifest (go.mod, package.json, requirements.txt, pom.xml, Cargo.toml)",
		},
	}
	out := RenderCompact(v)
	if !strings.Contains(out, "dependency: SKIPPED") {
		t.Errorf("expected a SKIPPED dependency line, got:\n%s", out)
	}
	if !strings.Contains(out, "no supported dependency manifest") {
		t.Errorf("expected the skip reason in the line, got:\n%s", out)
	}
}
