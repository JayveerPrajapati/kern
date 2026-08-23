package loop

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestAutonomyScoreBounds(t *testing.T) {
	cases := []AutonomyScore{
		{}, // all zeros → 0
		{Confidence: 1, RiskTolerance: 1, PolicyCoverage: 1, VerificationCoverage: 1, SafetyBudgetRatio: 1},
		{Confidence: 0.5, RiskTolerance: 0.3, PolicyCoverage: 0.8, VerificationCoverage: 0.6, SafetyBudgetRatio: 0.2},
		{Confidence: 2.0, RiskTolerance: -1.0, PolicyCoverage: 0.5, VerificationCoverage: 0.5, SafetyBudgetRatio: 0.5}, // out-of-range → clamped
	}
	for _, c := range cases {
		if s := c.Score(); s < 0 || s > 1 {
			t.Errorf("Score() = %.3f out of [0,1] for %+v", s, c)
		}
	}
}

func TestAutonomyScoreDeterministic(t *testing.T) {
	a := AutonomyScore{Confidence: 0.6, RiskTolerance: 0.5, PolicyCoverage: 0.7, VerificationCoverage: 0.8, SafetyBudgetRatio: 0.4}
	want := a.Score()
	for i := 0; i < 10; i++ {
		if got := a.Score(); got != want {
			t.Fatalf("Score() not deterministic: %v then %v", want, got)
		}
	}
}

func TestRecommendedLevelMapping(t *testing.T) {
	// Explicit boundary checks via RecommendedLevel on known scores.
	// Score = weighted mean. A uniform value v yields score v.
	for _, v := range []struct {
		v    float64
		want Autonomy
	}{
		{0.1, L0}, {0.3, L1}, {0.5, L2}, {0.7, L3}, {0.9, L4}, {0.96, L5},
	} {
		u := AutonomyScore{Confidence: v.v, RiskTolerance: v.v, PolicyCoverage: v.v, VerificationCoverage: v.v, SafetyBudgetRatio: v.v}
		if got := u.RecommendedLevel(); got != v.want {
			t.Errorf("uniform score %.2f → %s, want %s", v.v, got, v.want)
		}
	}
	// ensure the hard cap: RecommendedLevel never exceeds L5
	for i := 0; i < 100; i++ {
		if lvl := (AutonomyScore{Confidence: 1, RiskTolerance: 1, PolicyCoverage: 1, VerificationCoverage: 1, SafetyBudgetRatio: 1}).RecommendedLevel(); lvl > L5 {
			t.Fatalf("RecommendedLevel exceeded L5: %s", lvl)
		}
	}
}

func TestAutonomyScoreFromRiskOrdering(t *testing.T) {
	// A HIGH-risk change must recommend a LOWER (or equal) autonomy than a
	// low-risk change.
	low := AutonomyScoreFromRisk(domain.RiskLow).RecommendedLevel()
	med := AutonomyScoreFromRisk(domain.RiskMedium).RecommendedLevel()
	high := AutonomyScoreFromRisk(domain.RiskHigh).RecommendedLevel()
	crit := AutonomyScoreFromRisk(domain.RiskCritical).RecommendedLevel()
	if !(low >= med && med >= high && high >= crit) {
		t.Fatalf("risk→level ordering violated: low=%s med=%s high=%s crit=%s", low, med, high, crit)
	}
	if low != L4 {
		t.Errorf("RiskLow should recommend L4, got %s", low)
	}
	if high != L2 {
		t.Errorf("RiskHigh should recommend L2, got %s", high)
	}
}

func TestAllowedByScoreHardCap(t *testing.T) {
	// Score recommending L2: config L3/L4/L5 must be DENIED; config L0-L2 allowed.
	score := AutonomyScoreFromRisk(domain.RiskHigh) // → L2
	if L3.AllowedByScore(score) {
		t.Error("L3 must NOT be allowed when the score recommends L2")
	}
	if L5.AllowedByScore(score) {
		t.Error("L5 must NOT be allowed when the score recommends L2")
	}
	if !L2.AllowedByScore(score) {
		t.Error("L2 must be allowed when the score recommends L2")
	}
	if !L0.AllowedByScore(score) {
		t.Error("L0 must always be allowed")
	}
	// A perfect score still never grants beyond L5.
	if !L5.AllowedByScore(AutonomyScore{Confidence: 1, RiskTolerance: 1, PolicyCoverage: 1, VerificationCoverage: 1, SafetyBudgetRatio: 1}) {
		t.Error("L5 must be allowed when the score recommends L5")
	}
}
