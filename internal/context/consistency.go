package context

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// CheckConsistency detects contradictions between claims from different knowledge
// sources. Strict Plan Phase 14: "Conflicting information cannot silently
// produce a high-confidence engineering recommendation."
//
// The check groups claims by Scope (subject), then for each group, looks for
// pairs of claims from DIFFERENT sources that make contradicting statements.
// A contradiction is detected when two claims about the same subject:
//   - Come from different sources (e.g., GRAPH vs RUNTIME)
//   - Have different Statement text (after normalization)
//
// When conflicts are found, confidence is downgraded for the affected subjects.
func CheckConsistency(claims []domain.Claim) domain.ConsistencyReport {
	report := domain.ConsistencyReport{
		ConfidenceDowngrades: map[string]float64{},
	}

	// Group claims by Scope (subject).
	bySubject := map[string][]domain.Claim{}
	for _, c := range claims {
		subject := strings.TrimSpace(c.Scope)
		if subject == "" {
			subject = "_global_"
		}
		bySubject[subject] = append(bySubject[subject], c)
	}

	// For each subject, check for cross-source contradictions.
	for subject, group := range bySubject {
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				a, b := group[i], group[j]
				// Only check claims from DIFFERENT sources.
				if a.Source == b.Source {
					continue
				}
				// Check for contradiction: different statements about the same subject.
				if normalize(a.Statement) != normalize(b.Statement) {
					// Check if they're actually about the same thing (both reference the subject).
					report.Conflicts = append(report.Conflicts, domain.ConsistencyConflict{
						Subject:  subject,
						ClaimA:   a.Statement,
						SourceA:  domain.KnowledgeSource(a.Source),
						ClaimB:   b.Statement,
						SourceB:  domain.KnowledgeSource(b.Source),
					})
					// Downgrade confidence for this subject.
					currentDowngrade := report.ConfidenceDowngrades[subject]
					newConfidence := a.Confidence * 0.5 // conflict → halve confidence
					if b.Confidence*0.5 < newConfidence {
						newConfidence = b.Confidence * 0.5
					}
					if currentDowngrade == 0 || newConfidence < currentDowngrade {
						report.ConfidenceDowngrades[subject] = newConfidence
					}
				}
			}
		}
	}
	return report
}

// normalize lowercases and trims a statement for comparison. This is a simple
// heuristic — a production system would use semantic similarity.
func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}
