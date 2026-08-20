package evidence

import (
	"errors"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestNewBuilderSetsTypeAndStatement(t *testing.T) {
	b := NewBuilder(domain.ClaimFact, "the sky is blue")
	if b.claim.Type != domain.ClaimFact {
		t.Fatalf("Type = %q, want %q", b.claim.Type, domain.ClaimFact)
	}
	if b.claim.Statement != "the sky is blue" {
		t.Fatalf("Statement = %q, want %q", b.claim.Statement, "the sky is blue")
	}
}

func TestBuilderDefaults(t *testing.T) {
	b := NewBuilder(domain.ClaimRecommendation, "recommendation").Build()
	if b.Source != "" {
		t.Errorf("Source = %q, want empty", b.Source)
	}
	if b.Provenance != "" {
		t.Errorf("Provenance = %q, want empty", b.Provenance)
	}
	if b.Scope != "" {
		t.Errorf("Scope = %q, want empty", b.Scope)
	}
	if b.Confidence != 0 {
		t.Errorf("Confidence = %v, want 0", b.Confidence)
	}
	if len(b.Evidence) != 0 {
		t.Errorf("Evidence = %v, want none", b.Evidence)
	}
	if b.Timestamp.IsZero() {
		t.Error("Timestamp is zero; Build should set it to now")
	}
	if b.HasEvidence() {
		t.Error("HasEvidence() = true, want false")
	}
}

func TestBuilderChaining(t *testing.T) {
	b := NewBuilder(domain.ClaimHypothesis, "a hypothesis").
		WithSource("agent-1").
		WithProvenance("derived").
		WithScope("pkg.Foo").
		WithConfidence(0.5)
	if b.claim.Source != "agent-1" {
		t.Errorf("Source = %q, want agent-1", b.claim.Source)
	}
	if b.claim.Provenance != "derived" {
		t.Errorf("Provenance = %q, want derived", b.claim.Provenance)
	}
	if b.claim.Scope != "pkg.Foo" {
		t.Errorf("Scope = %q, want pkg.Foo", b.claim.Scope)
	}
	if b.claim.Confidence != 0.5 {
		t.Errorf("Confidence = %v, want 0.5", b.claim.Confidence)
	}
}

func TestBuilderWithEvidenceAppends(t *testing.T) {
	e1 := domain.Evidence{Type: domain.EvidenceTest, Content: "one"}
	e2 := domain.Evidence{Type: domain.EvidenceBuild, Content: "two"}
	b := NewBuilder(domain.ClaimFact, "fact").
		WithEvidence(e1).
		WithEvidence(e2)
	if len(b.claim.Evidence) != 2 {
		t.Fatalf("len(Evidence) = %d, want 2", len(b.claim.Evidence))
	}
	if b.claim.Evidence[0].Content != "one" {
		t.Errorf("Evidence[0] = %v, want one", b.claim.Evidence[0])
	}
	if b.claim.Evidence[1].Content != "two" {
		t.Errorf("Evidence[1] = %v, want two", b.claim.Evidence[1])
	}
}

func TestBuilderBuildReturnsCompleteClaim(t *testing.T) {
	ev := domain.Evidence{Type: domain.EvidenceGraph, Source: "intel", Content: "calls"}
	c := NewBuilder(domain.ClaimFact, "a fact").
		WithSource("sec").
		WithScope("svc").
		WithConfidence(1.0).
		WithEvidence(ev).
		Build()

	if c.Type != domain.ClaimFact {
		t.Errorf("Type = %q, want FACT", c.Type)
	}
	if c.Statement != "a fact" {
		t.Errorf("Statement = %q, want 'a fact'", c.Statement)
	}
	if c.Source != "sec" {
		t.Errorf("Source = %q, want sec", c.Source)
	}
	if c.Scope != "svc" {
		t.Errorf("Scope = %q, want svc", c.Scope)
	}
	if c.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", c.Confidence)
	}
	if len(c.Evidence) != 1 || c.Evidence[0] != ev {
		t.Errorf("Evidence = %v, want [%v]", c.Evidence, ev)
	}
	if c.Timestamp.IsZero() {
		t.Error("Timestamp is zero; Build should set it to now")
	}
	if !c.HasEvidence() {
		t.Error("HasEvidence() = false, want true")
	}
}

func TestBuilderBuildPanicsOnEmptyStatement(t *testing.T) {
	// Build panics when the statement is empty (documented behavior), so call
	// it in a deferred recover.
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("Build() did not panic on empty statement")
		}
	}()
	NewBuilder(domain.ClaimFact, "").Build()
}

// BuildChecked must reject evidence-free high-confidence RECOMMENDATIONs.
func TestBuildCheckedRejectsEvidenceFreeCriticalRecommendation(t *testing.T) {
	_, err := NewBuilder(domain.ClaimRecommendation, "you must revert the migration").
		WithSource("evidence").
		WithConfidence(ConfidenceHigh).
		BuildChecked()
	if err == nil {
		t.Fatal("BuildChecked() returned no error for an evidence-free high-confidence RECOMMENDATION")
	}
	if !errors.Is(err, ErrEvidenceFreeCritical) {
		t.Fatalf("error = %v, want ErrEvidenceFreeCritical", err)
	}
}

// BuildChecked allows a high-confidence RECOMMENDATION that carries evidence.
func TestBuildCheckedAllowsRecommendationWithEvidence(t *testing.T) {
	ev := domain.Evidence{Type: domain.EvidenceGraph, Source: "intel", Content: "call graph", Relationship: "calls"}
	c, err := NewBuilder(domain.ClaimRecommendation, "pin the dependency").
		WithSource("evidence").
		WithConfidence(ConfidenceHigh).
		WithEvidence(ev).
		BuildChecked()
	if err != nil {
		t.Fatalf("BuildChecked() returned error for an evidenced recommendation: %v", err)
	}
	if c.Type != domain.ClaimRecommendation || !c.HasEvidence() {
		t.Fatalf("unexpected claim: %+v", c)
	}
}

// TestBuildCheckedAllowsLowConfidenceRecommendation verifies the guard only
// applies at or above ConfidenceHigh.
func TestBuildCheckedAllowsLowConfidenceRecommendation(t *testing.T) {
	_, err := NewBuilder(domain.ClaimRecommendation, "consider refactoring").
		WithSource("evidence").
		WithConfidence(ConfidenceHigh - 0.1).
		BuildChecked()
	if err != nil {
		t.Fatalf("BuildChecked() returned error for a low-confidence RECOMMENDATION: %v", err)
	}
}

// TestBuildCheckedAllowsFactWithoutEvidence verifies FACTs do not require
// evidence.
func TestBuildCheckedAllowsFactWithoutEvidence(t *testing.T) {
	_, err := NewBuilder(domain.ClaimFact, "the sky is blue").
		WithSource("sec").
		WithConfidence(ConfidenceCertain).
		BuildChecked()
	if err != nil {
		t.Fatalf("BuildChecked() returned error for a FACT without evidence: %v", err)
	}
}

// TestRequireEvidence reports the guard for critical recommendations and is
// silent otherwise.
func TestRequireEvidence(t *testing.T) {
	if err := RequireEvidence(domain.Claim{
		Type:       domain.ClaimRecommendation,
		Statement:  "no evidence",
		Confidence: ConfidenceHigh,
	}); err == nil {
		t.Fatal("RequireEvidence() returned nil for an evidence-free high-confidence RECOMMENDATION")
	}

	good := domain.Claim{
		Type:       domain.ClaimRecommendation,
		Statement:  "has evidence",
		Confidence: ConfidenceHigh,
		Evidence:   []domain.Evidence{{Type: domain.EvidenceGraph, Content: "x"}},
	}
	if err := RequireEvidence(good); err != nil {
		t.Fatalf("RequireEvidence() returned error for an evidenced recommendation: %v", err)
	}

	low := domain.Claim{
		Type:       domain.ClaimRecommendation,
		Statement:  "low confidence",
		Confidence: ConfidenceHigh - 0.1,
	}
	if err := RequireEvidence(low); err != nil {
		t.Fatalf("RequireEvidence() returned error for a low-confidence recommendation: %v", err)
	}

	fact := domain.Claim{Type: domain.ClaimFact, Statement: "a fact", Confidence: ConfidenceCertain}
	if err := RequireEvidence(fact); err != nil {
		t.Fatalf("RequireEvidence() returned error for a FACT: %v", err)
	}
}
