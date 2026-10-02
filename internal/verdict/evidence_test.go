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

// TestVerifyStaticAnalysis runs Verify with ["static-analysis"] on the fixture
// module and asserts StaticAnalysis is non-nil and OK (the fixture has no vet
// issues).
