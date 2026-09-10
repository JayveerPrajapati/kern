package eval

import (
	"strings"
	"testing"
)

func TestAssertTokenReduction(t *testing.T) {
	a := AssertTokenReduction(0.3)
	a.evaluate(EvalResult{TokenReduction: 0.4})
	if a.Actual != 0.4 || !a.Pass {
		t.Errorf("0.4 >= 0.3 should pass: %+v", a)
	}
	b := AssertTokenReduction(0.5)
	b.evaluate(EvalResult{TokenReduction: 0.4})
	if b.Pass || b.Actual != 0.4 {
		t.Errorf("0.4 < 0.5 should fail: %+v", b)
	}
}

func TestAssertEvidenceRetention(t *testing.T) {
	a := AssertEvidenceRetention(0.8)
	a.evaluate(EvalResult{EvidenceRetention: 0.9})
	if !a.Pass || a.Actual != 0.9 {
		t.Errorf("0.9 >= 0.8 should pass: %+v", a)
	}
	b := AssertEvidenceRetention(0.9)
	b.evaluate(EvalResult{EvidenceRetention: 0.8})
	if b.Pass || b.Actual != 0.8 {
		t.Errorf("0.8 < 0.9 should fail: %+v", b)
	}
}

func TestAssertErrorRate(t *testing.T) {
	a := AssertErrorRate(0.2)
	a.evaluate(EvalResult{ErrorRate: 0.1})
	if !a.Pass || a.Actual != 0.1 {
		t.Errorf("0.1 <= 0.2 should pass: %+v", a)
	}
	b := AssertErrorRate(0.1)
	b.evaluate(EvalResult{ErrorRate: 0.2})
	if b.Pass || b.Actual != 0.2 {
		t.Errorf("0.2 > 0.1 should fail: %+v", b)
	}
}

func TestAssertionUnknownType(t *testing.T) {
	a := Assertion{Type: "bogus", Expected: 1.0}
	a.evaluate(EvalResult{})
	if a.Pass {
		t.Error("unknown assertion type should not pass")
	}
}

func TestRubricEvaluatedInRun(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "s1", Baseline: strings.Repeat("long baseline ", 20), Candidate: "short", CriticalEvidence: []string{"short"}},
	}, 0, []Assertion{
		AssertTokenReduction(0.5),
		AssertEvidenceRetention(1.0),
		AssertErrorRate(0.0),
	})
	res := h.Run()
	if len(res.Rubric) != 3 {
		t.Fatalf("Rubric = %d assertions, want 3", len(res.Rubric))
	}
	for i, a := range res.Rubric {
		if !a.Pass {
			t.Errorf("rubric[%d] (%s) should pass: %+v", i, a.Type, a)
		}
		switch a.Type {
		case "token_reduction":
			if a.Actual <= 0.5 {
				t.Errorf("token_reduction actual = %v, want > 0.5", a.Actual)
			}
		case "evidence_retention":
			if a.Actual != 1.0 {
				t.Errorf("evidence_retention actual = %v, want 1.0", a.Actual)
			}
		case "error_rate":
			if a.Actual != 0.0 {
				t.Errorf("error_rate actual = %v, want 0.0", a.Actual)
			}
		}
	}
}
