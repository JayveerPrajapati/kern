package context

import (
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// staleAfter is the freshness bound past which a claim's source is treated as
// stale for consistency purposes. Claims older than this cannot
// reliably confirm or deny a newer claim, so the subject is marked STALE rather
// than CONFLICT.
const staleAfter = 7 * 24 * time.Hour

// ApplyConsistency wires the exit gate into a context packet: it runs
// CheckConsistency over the packet's claims and, whenever the result is not a
// clean NO_CONFLICT, applies the confidence downgrades (never raising any
// claim's confidence) and attaches the report to the packet. A packet whose
// claims conflict is therefore never silently treated as certain — downstream
// consumers see the downgraded confidence and the report with the conflict
// explanation (14.4), staleness attribution (14.3), and overall result (14.2).
// NO_CONFLICT packets are left untouched (Consistency stays nil), so callers
// that checked for a nil report keep working.
func ApplyConsistency(pkt *domain.ContextPacket) {
	if pkt == nil {
		return
	}
	report := CheckConsistency(pkt.Facts)
	if report.Result == domain.ConflictNone {
		return
	}
	for i := range pkt.Facts {
		if nc, ok := report.ConfidenceDowngrades[pkt.Facts[i].Scope]; ok && pkt.Facts[i].Confidence > nc {
			pkt.Facts[i].Confidence = nc
		}
	}
	pkt.Consistency = &report
}

// CheckConsistency detects contradictions between claims from different knowledge
// sources. : "Conflicting information cannot silently
// produce a high-confidence engineering recommendation."
// The check groups claims by Scope (subject), then for each group, looks for
// pairs of claims from DIFFERENT sources that make contradicting statements.
// A contradiction is detected when two claims about the same subject:
// - Come from different sources (e.g., GRAPH vs RUNTIME)
// - Have different Statement text (after normalization)
// When conflicts are found, confidence is downgraded for the affected subjects.
// Additions: each conflict carries an Explanation (14.4) and a
// StaleSource when the contradiction is attributable to one side being stale
// (14.3); the report carries the overall ConflictResult enum (14.2).
func CheckConsistency(claims []domain.Claim) domain.ConsistencyReport {
	now := time.Now()
	report := domain.ConsistencyReport{
		ConfidenceDowngrades: map[string]float64{},
		Result:               domain.ConflictUnknown,
	}

	// Group claims by subject.
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
		if len(group) < 2 {
			continue
		}
		conflicted := false
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				a, b := group[i], group[j]
				// Only check claims from DIFFERENT sources.
				if a.Source == b.Source {
					continue
				}
				// Check for contradiction: different statements about the same subject.
				if normalize(a.Statement) == normalize(b.Statement) {
					continue // same statement: consistent
				}
				conflicted = true
				// Attribute staleness when one side is old.
				staleSource := ""
				if isStale(a.Timestamp, now) && !isStale(b.Timestamp, now) {
					staleSource = a.Source
				} else if isStale(b.Timestamp, now) && !isStale(a.Timestamp, now) {
					staleSource = b.Source
				}
				report.Conflicts = append(report.Conflicts, domain.ConsistencyConflict{
					Subject:     subject,
					ClaimA:      a.Statement,
					SourceA:     domain.KnowledgeSource(a.Source),
					ClaimB:      b.Statement,
					SourceB:     domain.KnowledgeSource(b.Source),
					StaleSource: staleSource,
					// Explain why the two claims conflict.
					Explanation: explainConflict(subject, a, b),
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
		if conflicted {
			report.Result = domain.ConflictPresent
		} else {
			report.Result = domain.ConflictNone
			// If the agreeing subject's evidence is stale, mark it.
			if isGroupStale(group, now) {
				report.StaleSubjects = append(report.StaleSubjects, subject)
				report.Result = domain.ConflictStale
			}
		}
	}

	if len(bySubject) == 0 {
		report.Result = domain.ConflictUnknown
	}
	return report
}

// isStale reports whether a claim is older than the staleness bound.
func isStale(ts time.Time, now time.Time) bool {
	return !ts.IsZero() && now.Sub(ts) > staleAfter
}

// isGroupStale reports whether every claim in a group is stale (all sources are
// too old to be authoritative).
func isGroupStale(group []domain.Claim, now time.Time) bool {
	if len(group) == 0 {
		return false
	}
	for _, c := range group {
		if !isStale(c.Timestamp, now) {
			return false
		}
	}
	return true
}

// explainConflict builds a human-readable explanation of why two claims
// contradict .
func explainConflict(subject string, a, b domain.Claim) string {
	return "subject " + subject + ": source " + a.Source + " (" + a.Statement + ") contradicts source " + b.Source + " (" + b.Statement + ")"
}

// normalize lowercases and trims a statement for comparison. This is a simple
// heuristic — a production system would use semantic similarity.
func normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}
