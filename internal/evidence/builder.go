package evidence

import (
	"errors"
	"fmt"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Builder constructs a domain.Claim with evidence attached using a fluent
// chain. Call Build() to obtain the finished claim.
type Builder struct {
	claim domain.Claim
}

// NewBuilder starts a new claim builder with the given type and statement.
func NewBuilder(claimType domain.ClaimType, statement string) *Builder {
	return &Builder{claim: domain.Claim{Type: claimType, Statement: statement}}
}

// WithSource sets who/what produced this claim (tool name, agent ID).
func (b *Builder) WithSource(source string) *Builder {
	b.claim.Source = source
	return b
}

// WithProvenance sets how the claim was derived.
func (b *Builder) WithProvenance(provenance string) *Builder {
	b.claim.Provenance = provenance
	return b
}

// WithScope sets what the claim applies to (symbol, file, service).
func (b *Builder) WithScope(scope string) *Builder {
	b.claim.Scope = scope
	return b
}

// WithConfidence sets the confidence score (0.0-1.0).
func (b *Builder) WithConfidence(c float64) *Builder {
	b.claim.Confidence = c
	return b
}

// WithEvidence attaches a piece of evidence.
func (b *Builder) WithEvidence(evidence domain.Evidence) *Builder {
	b.claim.Evidence = append(b.claim.Evidence, evidence)
	return b
}

// Build returns the completed Claim with Timestamp set to now. It panics if
// the statement is empty, so the programming error fails fast.
func (b *Builder) Build() domain.Claim {
	if b.claim.Statement == "" {
		panic("evidence: Build() called with an empty statement")
	}
	b.claim.Timestamp = time.Now()
	return b.claim
}

// ErrEvidenceFreeCritical is returned by BuildChecked when a claim would be an
// evidence-free critical recommendation.
var ErrEvidenceFreeCritical = errors.New("evidence: high-confidence RECOMMENDATION requires at least one evidence entry")

// RequireEvidence enforces the evidence-free critical recommendation guard: a
// RECOMMENDATION at or above ConfidenceHigh must carry at least one Evidence
// entry, returning a descriptive error otherwise or nil when allowed. FACTs
// and lower-confidence claims are unaffected.
func RequireEvidence(c domain.Claim) error {
	if c.Type == domain.ClaimRecommendation && c.Confidence >= ConfidenceHigh && !c.HasEvidence() {
		return fmt.Errorf("%w: type=%s confidence=%.2f statement=%q",
			ErrEvidenceFreeCritical, c.Type, c.Confidence, c.Statement)
	}
	return nil
}

// BuildChecked returns the completed Claim like Build but applies the
// evidence-free critical recommendation guard, returning an error instead of
// a claim when a high-confidence RECOMMENDATION has no evidence.
func (b *Builder) BuildChecked() (domain.Claim, error) {
	c := b.Build()
	if err := RequireEvidence(c); err != nil {
		return domain.Claim{}, err
	}
	return c, nil
}
