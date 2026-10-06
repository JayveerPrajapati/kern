package verdict

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestToEvidence asserts the evidence fields produced for a build result.
func TestToEvidence(t *testing.T) {
	now := time.Now()
	res := &VerificationResult{
		Verdict:     VerdictPass,
		Summary:     "build: PASS",
		GeneratedAt: now,
		Build:       &BuildResult{OK: true},
	}
	ev := toEvidence(res.Verdict, res)
	if ev.Source != "verification" {
		t.Errorf("expected source verification, got %q", ev.Source)
	}
	if ev.Type != domain.EvidenceBuild {
		t.Errorf("expected EvidenceBuild, got %q", ev.Type)
	}
	if ev.Digest == "" {
		t.Error("expected a non-empty digest")
	}
	if ev.Content == "" {
		t.Error("expected a non-empty content")
	}
}

// TestDeriveVerdictNoRunnerWarns pins F3: a test phase with no detected
// runner (StatusNoRunner) is never a plain PASS — it folds to WARN.
func TestDeriveVerdictNoRunnerWarns(t *testing.T) {
	res := &VerificationResult{
		UnitTests: &TestResult{
			OK:     true,
			Status: StatusNoRunner,
			Output: "skipped: no test runner detected (root has no go.mod)",
		},
	}
	if got := DeriveVerdict(res); got != VerdictWarn {
		t.Fatalf("verdict = %q, want WARN (never PASS)", got)
	}
}

// TestDeriveVerdictVetDiagnosticWarns pins F3: a diagnostic-only test run
// (StatusWarn, zero failed tests) folds to WARN, never FAIL.
func TestDeriveVerdictVetDiagnosticWarns(t *testing.T) {
	res := &VerificationResult{
		UnitTests: &TestResult{
			OK:      true,
			Status:  StatusWarn,
			Passed:  2838,
			Skipped: 152,
			Output:  "doctor_test.go:51: E2E gate test — full pipeline; runs in nightly non-short suite",
		},
	}
	if got := DeriveVerdict(res); got != VerdictWarn {
		t.Fatalf("verdict = %q, want WARN (never FAIL)", got)
	}
}

// TestAnnotate asserts the claim types and counts produced by Annotate.
func TestAnnotate(t *testing.T) {
	now := time.Now()
	res := &VerificationResult{
		Verdict:     VerdictWarn,
		Summary:     "security, build: WARN",
		GeneratedAt: now,
		Build:       &BuildResult{OK: true},
		Security:    &SecurityResult{Count: 2, Critical: 0, High: 1, OK: true},
	}
	claims := annotate(res)
	// build + security facts + inference = 3
	if len(claims) != 3 {
		t.Fatalf("expected 3 claims, got %d", len(claims))
	}
	facts := 0
	for _, c := range claims {
		if c.Type == domain.ClaimFact {
			facts++
		}
		if c.Confidence != 1.0 {
			t.Errorf("claim confidence should be 1.0, got %v", c.Confidence)
		}
	}
	if facts != 2 {
		t.Errorf("expected 2 FACT claims, got %d", facts)
	}
	last := claims[len(claims)-1]
	if last.Type != domain.ClaimInference {
		t.Errorf("expected final claim to be an inference, got %q", last.Type)
	}
	if !strings.Contains(last.Statement, string(res.Verdict)) {
		t.Errorf("inference should mention the verdict: %q", last.Statement)
	}
}

// TestDeriveVerdictDependencyWarningsWarn: dependency Warnings (rung 5
// advisory) fold to VerdictWarn when the check itself passes.
func TestDeriveVerdictDependencyWarningsWarn(t *testing.T) {
	res := &VerificationResult{
		Dependency: &DependencyResult{OK: true, Warnings: []string{"new dependency example.com/b (go.mod)"}},
	}
	if v := DeriveVerdict(res); v != VerdictWarn {
		t.Errorf("dependency warnings must yield WARN, got %s", v)
	}
}

// TestDeriveVerdictDependencyFailBeatsWarnings: warnings never flip OK to
// false, and a genuine dependency failure still fails despite warnings.
func TestDeriveVerdictDependencyFailBeatsWarnings(t *testing.T) {
	res := &VerificationResult{
		Dependency: &DependencyResult{OK: false, Warnings: []string{"new dependency example.com/b (go.mod)"}},
	}
	if v := DeriveVerdict(res); v != VerdictFail {
		t.Errorf("a dependency failure must still FAIL despite warnings, got %s", v)
	}
}

// TestDeriveVerdictReuseFindingsWarnNotFail: reuse findings fold to WARN and
// never to FAIL (advisory by design).
func TestDeriveVerdictReuseFindingsWarnNotFail(t *testing.T) {
	res := &VerificationResult{
		Reuse: &ReuseResult{OK: true, Findings: []string{"possible duplicate: a.go:5 x ≈ b.go:5 y (similarity 1.00)"}},
	}
	if v := DeriveVerdict(res); v != VerdictWarn {
		t.Errorf("reuse findings must yield WARN (never FAIL), got %s", v)
	}
}

// TestDeriveVerdictReuseSkippedStaysPass: Reuse.Skipped is INFORMATIONAL ONLY
// — it must NOT downgrade a PASS to VerdictSkipped (deliberately unlike the
// compliance checks).
func TestDeriveVerdictReuseSkippedStaysPass(t *testing.T) {
	res := &VerificationResult{
		Reuse: &ReuseResult{OK: true, Skipped: "reuse skipped: not a git repository"},
	}
	if v := DeriveVerdict(res); v != VerdictPass {
		t.Errorf("a skipped reuse check is informational and must stay PASS, got %s", v)
	}
}

// TestDeriveVerdictReuseCleanPasses: a clean reuse run (no findings, no skip)
// stays PASS.
func TestDeriveVerdictReuseCleanPasses(t *testing.T) {
	res := &VerificationResult{Reuse: &ReuseResult{OK: true}}
	if v := DeriveVerdict(res); v != VerdictPass {
		t.Errorf("a clean reuse run must stay PASS, got %s", v)
	}
}

// TestVerifyStaticAnalysis runs Verify with ["static-analysis"] on the fixture
// module and asserts StaticAnalysis is non-nil and OK (the fixture has no vet
// issues).
