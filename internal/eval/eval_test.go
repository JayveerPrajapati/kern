package eval

import (
	"strings"
	"testing"
)

func TestRunTokenReduction(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "s1", Baseline: strings.Repeat("the quick brown fox jumps over the lazy dog ", 20), Candidate: "the quick brown fox", CriticalEvidence: []string{"quick brown fox"}},
	}, 0, nil)
	res := h.Run()
	// Baseline is ~180 tokens, candidate ~4: reduction well above 0.7.
	if res.TokenReduction <= 0.7 {
		t.Errorf("TokenReduction = %v, want > 0.7", res.TokenReduction)
	}
	if res.Samples[0].BaselineTokens <= res.Samples[0].CandidateTokens {
		t.Errorf("baseline %d tokens should exceed candidate %d", res.Samples[0].BaselineTokens, res.Samples[0].CandidateTokens)
	}
}

func TestRunEvidenceRetention(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "s1", Baseline: "a b c", Candidate: "a c", CriticalEvidence: []string{"a", "b", "c"}},
	}, 0, nil)
	res := h.Run()
	if res.EvidenceRetention != 2.0/3.0 {
		t.Errorf("EvidenceRetention = %v, want %v", res.EvidenceRetention, 2.0/3.0)
	}
	if res.Samples[0].EvidenceRetained != 2 || res.Samples[0].EvidenceTotal != 3 {
		t.Errorf("sample retained/total = %d/%d, want 2/3", res.Samples[0].EvidenceRetained, res.Samples[0].EvidenceTotal)
	}
}

func TestRunBudgetFitting(t *testing.T) {
	long := strings.Repeat("word ", 500)
	h := NewEvalHarness([]Sample{
		{Name: "s1", Baseline: long, Candidate: long, CriticalEvidence: []string{"word"}},
	}, 100, nil)
	res := h.Run()
	if res.Samples[0].CandidateTokens > 100 {
		t.Errorf("candidate tokens %d > budget 100 after Fit", res.Samples[0].CandidateTokens)
	}
}

func TestRunPerSamplePassed(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "ok", Baseline: "long baseline text here", Candidate: "short", CriticalEvidence: []string{"short"}},
		{Name: "fail", Baseline: "long baseline text here", Candidate: "short", CriticalEvidence: []string{"missing fragment"}},
	}, 0, nil)
	res := h.Run()
	if len(res.Samples) != 2 {
		t.Fatalf("Samples = %d, want 2", len(res.Samples))
	}
	if !res.Samples[0].Passed {
		t.Error("sample 0 should pass (evidence retained, no budget)")
	}
	if res.Samples[1].Passed {
		t.Error("sample 1 should fail (evidence not retained)")
	}
	if res.ErrorRate != 0.5 {
		t.Errorf("ErrorRate = %v, want 0.5 (1 of 2 failed)", res.ErrorRate)
	}
}

func TestRunEmptyHarness(t *testing.T) {
	res := NewEvalHarness(nil, 0, nil).Run()
	if res.Score != 0 {
		t.Errorf("Score = %v, want 0 for empty harness", res.Score)
	}
	if res.TokenReduction != 0 || res.ErrorRate != 0 {
		t.Errorf("TokenReduction=%v ErrorRate=%v, want 0/0", res.TokenReduction, res.ErrorRate)
	}
	if res.EvidenceRetention != 1.0 {
		t.Errorf("EvidenceRetention = %v, want 1.0 (degenerate: no evidence)", res.EvidenceRetention)
	}
	if len(res.Samples) != 0 || len(res.Rubric) != 0 {
		t.Errorf("empty harness should have no samples/rubric, got %d/%d", len(res.Samples), len(res.Rubric))
	}
}

func TestRunScoreFormula(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "p1", Baseline: "long", Candidate: "x", CriticalEvidence: []string{"x"}},
		{Name: "p2", Baseline: "long", Candidate: "x", CriticalEvidence: []string{"x"}},
		{Name: "p3", Baseline: "long", Candidate: "x", CriticalEvidence: []string{"y"}},
		{Name: "p4", Baseline: "long", Candidate: "x", CriticalEvidence: []string{"y"}},
	}, 0, nil)
	res := h.Run()
	// retention = 2/4 = 0.5; error = 2/4 = 0.5; score = 100*(0.5*0.5 + 0.5*0.5) = 50.
	if res.Score != 50 {
		t.Errorf("Score = %v, want 50", res.Score)
	}
	if res.EvidenceRetention != 0.5 || res.ErrorRate != 0.5 {
		t.Errorf("retention=%v error=%v, want 0.5/0.5", res.EvidenceRetention, res.ErrorRate)
	}
}

func TestRunNegativeReduction(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "s1", Baseline: "short", Candidate: strings.Repeat("much longer candidate text ", 20)},
	}, 0, nil)
	res := h.Run()
	if res.TokenReduction >= 0 {
		t.Errorf("TokenReduction = %v, want negative (candidate larger than baseline)", res.TokenReduction)
	}
}

func TestRunReproducibleFlag(t *testing.T) {
	h := NewEvalHarness([]Sample{
		{Name: "s1", Baseline: "a", Candidate: "b"},
	}, 0, nil)
	if !h.Reproducible {
		t.Error("NewEvalHarness should set Reproducible=true")
	}
	res := h.Run()
	if !res.Reproducible {
		t.Error("Reproducible should be echoed in the result")
	}
}
