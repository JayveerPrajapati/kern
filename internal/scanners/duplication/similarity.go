package duplication

import (
	"math"
)

// Similarity computes a structural similarity score between two fingerprints,
// in the range [0.0, 1.0]. A score of 1.0 means the functions are structurally
// identical (same signature shape, same control flow, same call patterns, same
// size); 0.0 means no structural overlap.
//
// The score is a weighted combination of four signals (spec lines 1036-1049):
//   - Signature shape match (weight 0.20)
//   - Control-flow shape match (weight 0.35)
//   - Called-symbol set overlap (weight 0.30)
//   - Size/literal profile match (weight 0.15)
//
// Identifier names are intentionally NOT compared (spec line 1049).
//
// Size floor: functions below MinCandidateStatements (~5 lines / ~10 tokens)
// are ignored outright — getters, setters, one-liners and trivial wrappers
// are structurally similar to any other tiny helper by nature, not by
// duplication, and they were the dominant source of advisory noise (e.g. a
// 2-line clip vs headStr pair scoring 1.00). This replaces the former
// ≤3-statement discount: the floor is absolute, so tiny helpers are never
// candidates.
func Similarity(a, b Fingerprint) float64 {
	// Size floor: if either side is a tiny function, the pair is not a
	// duplication candidate regardless of how similar their shapes look.
	if a.StatementCount < MinCandidateStatements || b.StatementCount < MinCandidateStatements {
		return 0
	}

	sigScore := signatureSimilarity(a, b)
	cfScore := controlFlowSimilarity(a, b)
	callScore := calledSymbolsSimilarity(a, b)
	sizeScore := sizeSimilarity(a, b)

	raw := 0.20*sigScore + 0.35*cfScore + 0.30*callScore + 0.15*sizeScore

	return raw
}

// signatureSimilarity compares signature shape.
func signatureSimilarity(a, b Fingerprint) float64 {
	if a.SignatureShape == b.SignatureShape {
		return 1.0
	}
	// Partial credit for matching param/return counts even if type shapes differ.
	score := 0.0
	if a.ParamCount == b.ParamCount {
		score += 0.5
	}
	if a.ReturnCount == b.ReturnCount {
		score += 0.5
	}
	return score
}

// controlFlowSimilarity compares the control-flow shape using cosine similarity
// over the CF vector.
func controlFlowSimilarity(a, b Fingerprint) float64 {
	va := cfVector(a.ControlFlow)
	vb := cfVector(b.ControlFlow)
	return cosine(va, vb)
}

// cfVector converts a CFFingerprint into a float64 vector for comparison.
func cfVector(cf CFFingerprint) []float64 {
	return []float64{
		float64(cf.IfCount),
		float64(cf.ForCount),
		float64(cf.RangeCount),
		float64(cf.SwitchCount),
		float64(cf.ReturnCount),
		float64(cf.DeferCount),
		float64(cf.GoCount),
		float64(cf.AssignCount),
		float64(cf.CallCount),
	}
}

// calledSymbolsSimilarity compares the set of called symbols using Jaccard
// overlap (normalized by arity).
func calledSymbolsSimilarity(a, b Fingerprint) float64 {
	if len(a.CalledSymbols) == 0 && len(b.CalledSymbols) == 0 {
		// Both call nothing — this is absence of evidence, not evidence of
		// similarity. Return neutral (0.5) so it doesn't inflate the score
		// for trivial functions (getters, setters) that call nothing.
		return 0.5
	}
	setA := make(map[string]bool, len(a.CalledSymbols))
	for _, s := range a.CalledSymbols {
		setA[s] = true
	}
	setB := make(map[string]bool, len(b.CalledSymbols))
	for _, s := range b.CalledSymbols {
		setB[s] = true
	}
	intersection := 0
	for s := range setA {
		if setB[s] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0.5
	}
	return float64(intersection) / float64(union)
}

// sizeSimilarity compares the size profile (statement count + literal count)
// using a Gaussian decay so minor differences don't penalize too harshly.
func sizeSimilarity(a, b Fingerprint) float64 {
	stmtScore := gaussianDecay(float64(a.StatementCount), float64(b.StatementCount), 5.0)
	litScore := gaussianDecay(float64(a.LiteralCount), float64(b.LiteralCount), 3.0)
	return 0.7*stmtScore + 0.3*litScore
}

// gaussianDecay returns 1.0 when a==b, decaying as they differ. sigma controls
// how quickly the score drops.
func gaussianDecay(a, b, sigma float64) float64 {
	diff := a - b
	return math.Exp(-(diff * diff) / (2 * sigma * sigma))
}

// cosine computes the cosine similarity between two vectors.
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float64
	for i := range a {
		dot += a[i] * b[i]
		magA += a[i] * a[i]
		magB += b[i] * b[i]
	}
	if magA == 0 || magB == 0 {
		// Both zero vectors = both functions have no control flow (getters,
		// setters, simple returns). This is absence of evidence, not evidence
		// of similarity. Return neutral (0.5) so trivial functions don't get
		// inflated similarity scores.
		if magA == 0 && magB == 0 {
			return 0.5
		}
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// Bucket classifies a similarity score into the spec's tiers (lines 1028-1032).
//
//	< 0.60      → "ignore"
//	0.60-0.85   → "informational"
//	0.85-0.95   → "warning"
//	> 0.95      → "block-candidate"
//
// "block-candidate" does NOT mean the check blocks — it means the score is high
// enough to be a candidate for future blocking once benchmarks justify it
// (spec line 1084). The check itself stays at WARN.
func Bucket(score float64) string {
	switch {
	case score > 0.95:
		return "block-candidate"
	case score >= 0.85:
		return "warning"
	case score >= 0.60:
		return "informational"
	default:
		return "ignore"
	}
}

// DetectionThreshold is the minimum similarity at/above which the in-house
// check emits an advisory finding. Raised from the spec's 0.60 tier to 0.95
// (the "block-candidate" tier): the 0.60/0.85 tiers are below the precision
// the in-house oracle can back (benchmark precision 0.50 at 0.60), so they
// only printed noise (e.g. "clip vs headStr similarity 1.00"). Findings at
// this level are advisory WARN only — the triage layer of the two-pass model.
// Because DetectionThreshold sits above BlockCandidateThreshold, every
// emitted advisory is automatically block-eligible; the 0.60-0.95 band is
// deliberately not printed (the full jscpd two-pass run in CI still scans it).
const DetectionThreshold = 0.95

// MinCandidateStatements is the size floor for duplication candidates: a
// function below this statement count (~5 lines / ~10 tokens, a Go statement
// averages about two tokens) is a tiny helper (getter, setter, one-line
// wrapper, trivial guard) that is structurally similar to any other tiny
// helper by nature, not by duplication. Similarity returns 0 for any pair
// where either side is below the floor, so tiny helpers never become
// candidates or produce advisory noise.
const MinCandidateStatements = 5

// MaxAdvisoryFindings caps how many in-house advisory findings a single check
// run emits. Above the cap the findings are truncated and one summary finding
// reports how many more candidates exist (the full set is visible in a
// non-fast run or the CI duplication report).
const MaxAdvisoryFindings = 20

// BlockCandidateThreshold is the similarity ABOVE which an in-house finding
// is a block-eligible candidate in the two-pass triage model (orchestrated by
// adapters/jscpd): candidates at this level MAY escalate to BLOCK, but only
// when jscpd independently confirms a clone in the same file pair. It sits
// above the 0.85 "warning" tier so only near-certain structural matches are
// candidates. With DetectionThreshold (0.95) raised above it, every advisory
// the in-house check actually prints is already above this gate; the constant
// remains the eligibility line for direct Similarity callers and the
// blocking-path benchmark.
const BlockCandidateThreshold = 0.90

// BlockEligible reports whether a similarity score is a block-eligible
// candidate (> BlockCandidateThreshold) for two-pass jscpd confirmation.
func BlockEligible(score float64) bool { return score > BlockCandidateThreshold }
