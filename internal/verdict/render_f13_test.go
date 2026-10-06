package verdict

import (
	"strings"
	"testing"
)

// F13: the compact render of a failed test step shows the FAILING tests'
// excerpts only — passing-test spam is suppressed — and points at the audit
// log that holds the full output.
func TestRenderCompactFailureExcerptAndLogPointer(t *testing.T) {
	log := `=== RUN   TestOne
--- PASS: TestOne (0.00s)
=== RUN   TestTwo
--- PASS: TestTwo (0.00s)
=== RUN   TestBroken
    x_test.go:9: boom
--- FAIL: TestBroken (0.00s)
FAIL
FAIL	example.com/m	0.5s
`
	v := VerificationResult{
		Verdict:   VerdictFail,
		UnitTests: &TestResult{OK: false, Passed: 2, Failed: 1, Output: log, LogPath: ".kern/audit/20261004-090000.000000001/verify-test.log"},
	}
	out := RenderCompact(v)

	if !strings.Contains(out, "failed tests: TestBroken") {
		t.Fatalf("failed test must be named in:\n%s", out)
	}
	if !strings.Contains(out, "--- FAIL: TestBroken") || !strings.Contains(out, "x_test.go:9: boom") {
		t.Fatalf("failing test excerpt must be shown in:\n%s", out)
	}
	if strings.Contains(out, "--- PASS") || strings.Contains(out, "=== RUN") {
		t.Fatalf("passing-test spam must be suppressed in:\n%s", out)
	}
	if !strings.Contains(out, "full log: .kern/audit/20261004-090000.000000001/verify-test.log") {
		t.Fatalf("full-output pointer must be present in:\n%s", out)
	}
}

// No audit log written: no pointer, no fabricated path.
func TestRenderCompactNoLogNoPointer(t *testing.T) {
	v := VerificationResult{
		Verdict:   VerdictFail,
		UnitTests: &TestResult{OK: false, Passed: 0, Failed: 1, Output: "--- FAIL: TestBroken (0.00s)\n"},
	}
	if out := RenderCompact(v); strings.Contains(out, "full log:") {
		t.Fatalf("no LogPath must mean no pointer, got:\n%s", out)
	}
}

// A failed run whose output is ONLY passing-test noise (failure came from
// the exit status, not a --- FAIL marker) keeps the bounded raw tail —
// never an empty excerpt.
func TestBoundedFailureOutputAllNoiseFallsBackToTail(t *testing.T) {
	long := strings.Repeat("--- PASS: TestX (0.00s)\n", 2000)
	out := boundedFailureOutput(long)
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("all-noise failure must fall back to the bounded tail, got:\n%s", out[:200])
	}
}
