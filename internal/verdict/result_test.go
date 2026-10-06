package verdict

import (
	"strings"
	"testing"
	"time"
)

// TestRenderCompactVetDiagnosticWarnsNotFails pins F3 defect 1: a
// diagnostic-only test run (zero failed tests, go vet finding) renders the
// phase as WARN with the diagnostic, never as FAILED — and a no-runner skip
// renders SKIPPED, never a passing phase line.
func TestRenderCompactVetDiagnosticWarnsNotFails(t *testing.T) {
	vet := VerificationResult{
		Verdict: VerdictWarn,
		UnitTests: &TestResult{
			OK:      true,
			Status:  StatusWarn,
			Passed:  2838,
			Skipped: 152,
			Output:  "doctor_test.go:51: E2E gate test — full pipeline; runs in nightly non-short suite",
		},
	}
	out := RenderCompact(vet)
	if !strings.Contains(out, "tests: WARN (diagnostic: doctor_test.go:51: E2E gate test") {
		t.Errorf("RenderCompact must surface the vet diagnostic as WARN:\n%s", out)
	}
	if strings.Contains(out, "tests: FAILED") || strings.Contains(out, "tests: FAIL ") {
		t.Errorf("a diagnostic-only run must never render a test failure:\n%s", out)
	}

	noRunner := VerificationResult{
		Verdict: VerdictWarn,
		UnitTests: &TestResult{
			OK:     true,
			Status: StatusNoRunner,
			Output: "skipped: no test runner detected (root has no go.mod)",
		},
	}
	out = RenderCompact(noRunner)
	if !strings.Contains(out, "tests: SKIPPED skipped: no test runner detected") {
		t.Errorf("no-runner phase must render SKIPPED with the reason:\n%s", out)
	}
	if strings.Contains(out, "verdict: PASS") {
		t.Errorf("no-runner run must never claim PASS:\n%s", out)
	}
}

// TestRenderCompactStaticAnalysisSkipped pins the static-analysis
// did-not-run fix: a phase whose tool could NOT be executed renders
// "static-analysis: SKIPPED <reason>", never "FAIL tool=go vet findings=0"
// (an unmeasured run is a skip, not a false FAIL — and never a clean OK).
func TestRenderCompactStaticAnalysisSkipped(t *testing.T) {
	skipped := VerificationResult{
		Verdict: VerdictSkipped,
		StaticAnalysis: &StaticAnalysisResult{
			OK:     false,
			Status: StatusSkipped,
			Tool:   "go vet",
			Output: "static-analysis not executed: go vet could not run (exit 1): exec: \"go\": executable file not found in $PATH; ensure go vet is installed and runnable",
		},
	}
	out := RenderCompact(skipped)
	if !strings.Contains(out, "static-analysis: SKIPPED static-analysis not executed: go vet could not run (exit 1)") {
		t.Errorf("a did-not-run static analysis must render SKIPPED with the reason:\n%s", out)
	}
	if strings.Contains(out, "static-analysis: FAIL") || strings.Contains(out, "findings=0") {
		t.Errorf("a did-not-run static analysis must never render FAIL:\n%s", out)
	}
	if strings.Contains(out, "verdict: PASS") {
		t.Errorf("a skipped static analysis must never claim PASS:\n%s", out)
	}
}

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

func TestRenderCompactSecurityLiveFindingsWarnNotOK(t *testing.T) {
	live := VerificationResult{Verdict: VerdictPassWithWarning, Security: &SecurityResult{OK: true, Count: 3, High: 2, Low: 1}}
	if out := RenderCompact(live); !strings.Contains(out, "security: WARN ") || strings.Contains(out, "security: OK") {
		t.Errorf("live findings must render WARN, not OK:\n%s", out)
	}
	suppressedOnly := VerificationResult{Verdict: VerdictPass, Security: &SecurityResult{OK: true, Count: 2, Suppressed: 2}}
	if out := RenderCompact(suppressedOnly); !strings.Contains(out, "security: OK ") {
		t.Errorf("suppressed-only findings must stay OK:\n%s", out)
	}
	clean := VerificationResult{Verdict: VerdictPass, Security: &SecurityResult{OK: true}}
	if out := RenderCompact(clean); !strings.Contains(out, "security: OK ") {
		t.Errorf("clean scan must stay OK:\n%s", out)
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
					Severity: "low",
					Rule:     "ip-leak",
					Message:  "unencrypted IP reference",
				},
			},
		},
	}
	out := RenderCompact(v)
	want := "auth/login.go:42 [low] ip-leak: unencrypted IP reference"
	if !strings.Contains(out, want) {
		t.Errorf("RenderCompact missing finding detail %q in:\n%s", want, out)
	}
}

// TestRenderCompactIncludesFailureOutput (F-4 + D1): a failed check whose
// Output carries the actionable reason must surface that text after the
// status line instead of a bare FAIL — and when the failure is a
// build/vet-phase error (zero failed tests), the reason is folded into the
// status line itself so "tests: FAIL passed=0 failed=0" can never read as a
// contradiction (audit D1).
func TestRenderCompactIncludesFailureOutput(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictFail,
		UnitTests: &TestResult{
			OK:     false,
			Passed: 0,
			Failed: 0,
			Output: "# github.com/x/repositories\nrepositories/foo.go:12:34: conversion from int64 to string (int64)\nFAIL\tgithub.com/x/repositories [build failed]\nFAIL\n",
		},
	}
	out := RenderCompact(v)
	for _, want := range []string{
		"tests: FAILED (diagnostic: repositories/foo.go:12:34: conversion from int64 to string (int64)) passed=0 failed=0 skipped=0",
		"# github.com/x/repositories",
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

// TestRenderCompactIntegrationFailureOutput (F-4 + D1): Integration reuses
// TestResult, which carries Output; it must surface it on failure as well,
// and a build-phase failure (zero failed tests) folds the reason into the
// status line exactly like the UnitTests branch.
func TestRenderCompactIntegrationFailureOutput(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictFail,
		Integration: &TestResult{
			OK:     false,
			Passed: 0,
			Failed: 0,
			Output: "# github.com/x/integ\nintegration/x_test.go:4:2: undefined: helper\nFAIL\tgithub.com/x/integ [build failed]\nFAIL\n",
		},
	}
	out := RenderCompact(v)
	for _, want := range []string{
		"integration: FAILED (diagnostic: integration/x_test.go:4:2: undefined: helper) passed=0 failed=0 skipped=0",
		"# github.com/x/integ",
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

// TestRenderCompactSecretsFindingsWarn pins F7's renderer contradiction: a
// secrets scan that FOUND 63 secrets must read "secrets: WARN findings=63",
// never the old "secrets: OK findings=63" (findings are findings, not OK).
func TestRenderCompactSecretsFindingsWarn(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictWarn,
		Secrets: &SecretsResult{
			OK:       true, // advisory: findings never flip OK
			Count:    63,
			Detail:   "found 63 secret(s) in commit history",
			Findings: []SecretFinding{{Commit: "abc1234", File: "config.txt", Line: 2, Kind: "aws-access-key", Snippet: "AKIA…MPLE"}},
		},
	}
	out := RenderCompact(v)
	if !strings.Contains(out, "secrets: WARN findings=63") {
		t.Errorf("RenderCompact must render findings as WARN, got:\n%s", out)
	}
	if strings.Contains(out, "secrets: OK") {
		t.Errorf("RenderCompact must not render a findings-carrying secrets check as OK:\n%s", out)
	}
	if !strings.Contains(out, "abc1234 config.txt:2 [aws-access-key] AKIA…MPLE") {
		t.Errorf("RenderCompact must still list the finding detail:\n%s", out)
	}
}

// TestRenderCompactCVEFindingsWarn is the CVE analogue: reported
// vulnerabilities render as WARN, not OK.
func TestRenderCompactCVEFindingsWarn(t *testing.T) {
	v := VerificationResult{
		Verdict: VerdictWarn,
		CVE: &CVEResult{
			OK:       true, // advisory
			Count:    2,
			Findings: []CVEFinding{{ID: "GO-2023-1234", Module: "example.com/x", Summary: "summary"}},
		},
	}
	out := RenderCompact(v)
	if !strings.Contains(out, "cve: WARN vulnerabilities=2") {
		t.Errorf("RenderCompact must render vulnerabilities as WARN, got:\n%s", out)
	}
	if strings.Contains(out, "cve: OK") {
		t.Errorf("RenderCompact must not render a vulnerabilities-carrying CVE check as OK:\n%s", out)
	}
}
