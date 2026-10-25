package metaroute

import (
	"math"
	"testing"
)

// TestMetaCosSimMathCorrect pins the metaCosSim denominator against the
// 2026-10-25 sqrtF regression: the hand-rolled 3-iteration Newton square
// root under-converged for large |q|·|t| products (sqrtF(92) ≈ 14.02 vs
// true 9.59), silently under-scoring every long catalog description — a
// fattened description dropped below the 0.16 semantic threshold with the
// same token overlap. The cosine must be overlap/sqrt(|q|·|t|), exactly.
func TestMetaCosSimMathCorrect(t *testing.T) {
	q := []string{"run", "full", "verification", "repo"}
	// 23 distinct tokens — the size of a fattened kern_run description.
	tt := []string{
		"kern", "run", "intent", "through", "full", "task", "pipeline",
		"compile", "select", "workflow", "capabiliti", "agent", "create",
		"policy", "preflight", "return", "result", "risk", "approval",
		"caps", "tool", "next", "step",
	}
	got := metaCosSim(q, tt)
	want := 2 / math.Sqrt(float64(len(q))*float64(len(tt)))
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("metaCosSim = %v, want %v (sqrt must be math-exact)", got, want)
	}
	if got < 0.16 {
		t.Fatalf("metaCosSim = %v must clear the 0.16 semantic threshold for a 2-token overlap", got)
	}
}

// TestSemanticRouteSurvivesDescriptionGrowth pins the end-to-end regression:
// the imperative verify request must keep routing (kern_verify or kern_run)
// even when kern_run's catalog description grows to the fattened variant —
// routing must depend on token overlap, not on description length crushing
// the cosine through an inaccurate denominator.
func TestSemanticRouteSurvivesDescriptionGrowth(t *testing.T) {
	// Simulate the fattened-description scoring directly: the query tokens
	// against a 23-token tool vector (the live kern_run description size).
	// The route must clear the threshold — pinned by the cosine test above —
	// and the classifier's public contract is pinned separately by
	// TestClassifyMetaRequest_ImperativeVerifyUnaffected.
	if _, _, score, ok := SemanticMetaRoute("run a full verification of this repo"); !ok {
		t.Fatal("imperative verify request lost its semantic route — description growth must not drop it below threshold")
	} else if score < 0.16 {
		t.Fatalf("semantic score %v below threshold 0.16", score)
	}
}
