package evidence

import (
	"fmt"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Conflict is a deterministic potential contradiction between two claims.
// Both claims are ALWAYS preserved (never merged) — consumers decide how to
// surface conflicts (e.g. only in debug/review modes, or when the claims'
// confidence crosses a risk threshold).
type Conflict struct {
	ClaimA domain.Claim
	ClaimB domain.Claim
	Reason string
}

// DetectConflicts finds pairs (i < j) of claims with the SAME non-empty
// Scope, both with Confidence >= ConfidenceHigh (0.9), whose Statements
// differ. Deterministic order (slice order); each unordered pair reported
// once. Unscoped claims (Scope == "") never conflict.
func DetectConflicts(claims []domain.Claim) []Conflict {
	var out []Conflict
	for i := 0; i < len(claims); i++ {
		a := claims[i]
		if a.Scope == "" || a.Confidence < ConfidenceHigh {
			continue
		}
		for j := i + 1; j < len(claims); j++ {
			b := claims[j]
			if b.Scope != a.Scope || b.Confidence < ConfidenceHigh {
				continue
			}
			if a.Statement == b.Statement {
				continue
			}
			out = append(out, Conflict{
				ClaimA: a,
				ClaimB: b,
				Reason: fmt.Sprintf("conflicting high-confidence claims for scope %s", a.Scope),
			})
		}
	}
	return out
}
