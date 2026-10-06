package verdict

import (
	"strings"
	"testing"
)

func TestTestFailureReasonIgnoresTruncatedLogFragments(t *testing.T) {
	out := "tCheck_DegradedMode_KernMissing\n--- PASS: TestCheck_DegradedMode_KernMissing (3.00s)\nflag provided but not defined: -bogus\n"
	if got := testFailureReason(out); got != "" {
		t.Fatalf("fragment of a passing log must not be reported as the reason, got %q", got)
	}
}

func TestTestFailureReasonFindsDiagnosticAfterNoise(t *testing.T) {
	out := "some fragment\nrepositories/foo.go:12:34: undefined: x\n"
	if got := testFailureReason(out); got != "repositories/foo.go:12:34: undefined: x" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderCompactNamesFailedTestsBeyondTheTail(t *testing.T) {
	log := "--- FAIL: TestBroken (0.01s)\n    x_test.go:9: boom\n" + strings.Repeat("--- PASS: TestOk (0.00s)\n", 3000)
	v := VerificationResult{
		Verdict:   VerdictFail,
		UnitTests: &TestResult{OK: false, Passed: 3000, Failed: 1, Output: log},
	}
	out := RenderCompact(v)
	if !strings.Contains(out, "failed tests: TestBroken") {
		t.Fatalf("failed test not named in:\n%s", out)
	}
}

func TestRenderCompactBoundsFailureOutput(t *testing.T) {
	long := strings.Repeat("--- PASS: TestX (0.00s)\n", 2000)
	v := VerificationResult{
		Verdict:   VerdictFail,
		UnitTests: &TestResult{OK: false, Passed: 2000, Output: long},
	}
	out := RenderCompact(v)
	if len(out) > maxFailureOutput+500 {
		t.Fatalf("failure output not bounded: %d bytes", len(out))
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("missing truncation marker in:\n%s", out)
	}
}
