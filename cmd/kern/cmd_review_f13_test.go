package main

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/verdict"
)

// F13: the default `kern verify` render shows counts plus the FAILING tests'
// excerpts only — passing-test spam is suppressed and a pointer to the audit
// log (which holds the full output) is printed whenever one was written.
func TestVerifyTestsDetailShowsFailuresOnly(t *testing.T) {
	out := `=== RUN   TestAddPasses
--- PASS: TestAddPasses (0.00s)
=== RUN   TestAddPasses2
--- PASS: TestAddPasses2 (0.00s)
=== RUN   TestAddFails
    add_test.go:9: Add(1,1) unexpectedly equals 2
--- FAIL: TestAddFails (0.00s)
FAIL
FAIL	fix13	1.159s
`
	tr := &verdict.TestResult{OK: false, Output: out, LogPath: ".kern/audit/20261004-090000.000000001/verify-test.log"}
	lines := verifyTestsDetail(tr)
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "--- FAIL: TestAddFails") {
		t.Fatalf("failing test marker must be shown, got: %s", joined)
	}
	if !strings.Contains(joined, "add_test.go:9: Add(1,1) unexpectedly equals 2") {
		t.Fatalf("failing test diagnostic must be shown, got: %s", joined)
	}
	if strings.Contains(joined, "--- PASS") || strings.Contains(joined, "=== RUN") {
		t.Fatalf("passing-test spam must be suppressed, got: %s", joined)
	}
	if !strings.Contains(joined, "full log: .kern/audit/20261004-090000.000000001/verify-test.log") {
		t.Fatalf("full-output pointer must be present, got: %s", joined)
	}
}

// A green run prints no test output at all (passing spam suppressed); the
// pointer remains so the full log is still reachable.
func TestVerifyTestsDetailGreenRunSuppressesOutput(t *testing.T) {
	out := `=== RUN   TestAddPasses
--- PASS: TestAddPasses (0.00s)
PASS
ok  	fix13	1.159s
`
	tr := &verdict.TestResult{OK: true, Output: out, LogPath: ".kern/audit/20261004-090000.000000002/verify-test.log"}
	lines := verifyTestsDetail(tr)

	if len(lines) != 1 || !strings.HasPrefix(lines[0], "full log: ") {
		t.Fatalf("green run must print only the full-log pointer, got: %q", lines)
	}
}

// No audit log written (write failed or empty run): no pointer, no
// fabricated path.
func TestVerifyTestsDetailNoLogNoPointer(t *testing.T) {
	tr := &verdict.TestResult{OK: true, Output: "--- PASS: TestX (0.00s)\n"}
	if lines := verifyTestsDetail(tr); len(lines) != 0 {
		t.Fatalf("no LogPath must mean no pointer, got: %q", lines)
	}
}

// A large failure is line-bounded with an honest more-lines note.
func TestTestFailureExcerptBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("--- FAIL: TestMany\n")
	}
	got := verdict.TestFailureExcerpt(b.String(), 40)
	if n := strings.Count(got, "--- FAIL"); n != 40 {
		t.Fatalf("excerpt must be bounded to 40 lines, got %d", n)
	}
	if !strings.Contains(got, "... 60 more lines in the full log") {
		t.Fatalf("bounded excerpt must say how many lines were cut, got: %s", got)
	}
}

// A non-Go runner's output has no go-test noise prefixes, so the excerpt
// keeps it verbatim (still bounded) — never silently empty.
func TestTestFailureExcerptNonGoRunnerKept(t *testing.T) {
	got := verdict.TestFailureExcerpt("1 failed, 3 passed in 0.5s\nE   AssertionError: boom\n", 40)
	if !strings.Contains(got, "E   AssertionError: boom") {
		t.Fatalf("non-Go failure output must be kept, got: %s", got)
	}
}
