package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/runtime"
)

// TestFreshnessAdjustedEvidenceConfidence verifies the confidence
// scaling: fresh evidence leaves the confidence unchanged (factor 1.0), stale
// evidence scales it down to 0.5, and no evidence is a no-op.
func TestFreshnessAdjustedEvidenceConfidence(t *testing.T) {
	now := time.Now()
	bound := 24 * time.Hour

	freshEv := []domain.Evidence{{Timestamp: now.Add(-time.Hour)}}
	if got := FreshnessAdjustedConfidence(1.0, freshEv, now, bound); got != 1.0 {
		t.Errorf("fresh confidence = %.2f, want 1.0", got)
	}

	staleEv := []domain.Evidence{{Timestamp: now.Add(-48 * time.Hour)}}
	if got := FreshnessAdjustedConfidence(1.0, staleEv, now, bound); got != 0.5 {
		t.Errorf("stale confidence = %.2f, want 0.5", got)
	}

	// No evidence: unchanged.
	if got := FreshnessAdjustedConfidence(0.7, nil, now, bound); got != 0.7 {
		t.Errorf("no-evidence confidence = %.2f, want 0.7", got)
	}
}

// TestFreshnessAdjustedRisk verifies the risk scaling: stale
// evidence applies the 0.5 multiplier to the score and appends a
// "freshness:stale" factor; fresh evidence leaves the risk unchanged.
func TestFreshnessAdjustedRisk(t *testing.T) {
	now := time.Now()
	bound := 24 * time.Hour

	base := domain.Risk{Level: domain.RiskMedium, Score: 0.8, Factors: []string{"governance:source:write"}}

	fresh := FreshnessAdjustedRisk(base, []domain.Evidence{{Timestamp: now.Add(-time.Hour)}}, now, bound)
	if fresh.Score != 0.8 {
		t.Errorf("fresh score = %.2f, want 0.8 unchanged", fresh.Score)
	}
	if len(fresh.Factors) != len(base.Factors) {
		t.Errorf("fresh factors = %v, want unchanged %v", fresh.Factors, base.Factors)
	}

	stale := FreshnessAdjustedRisk(base, []domain.Evidence{{Timestamp: now.Add(-48 * time.Hour)}}, now, bound)
	if stale.Score != 0.4 {
		t.Errorf("stale score = %.2f, want 0.4 (0.8 * 0.5)", stale.Score)
	}
	if stale.Level != domain.RiskMedium {
		t.Errorf("stale level = %q, want MEDIUM preserved", stale.Level)
	}
	hasFactor := false
	for _, f := range stale.Factors {
		if f == "freshness:stale" {
			hasFactor = true
		}
	}
	if !hasFactor {
		t.Errorf("stale factors = %v, want a freshness:stale factor", stale.Factors)
	}
}

// TestEngineFreshnessScoringOptIn proves the opt-in flag actually
// changes the assembled packet: with stale runtime evidence, the risk scores
// differ from the default (no-freshness) path, while the default path is
// unchanged.
func TestEngineFreshnessScoringOptIn(t *testing.T) {
	now := time.Now()
	// A runtime source with a stale error event (older than the default 7-day
	// bound) so RuntimeEvidence is STALE.
	src := runtime.NewStore()
	src.Ingest(runtime.Event{
		ID:        "stale-1",
		Type:      runtime.EventError,
		Severity:  "error",
		Message:   "old incident",
		Timestamp: now.Add(-30 * 24 * time.Hour),
	})

	baseline := testEngine(t).WithRuntimeSource(src)
	defaultPkt, err := baseline.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("default AnalyzeChange(Foo): %v", err)
	}
	if len(defaultPkt.RuntimeEvidence) == 0 {
		t.Fatal("baseline runtime evidence empty; want the stale event surfaced")
	}

	fresh := testEngine(t).WithRuntimeSource(src).WithFreshnessScoring(true)
	optInPkt, err := fresh.AnalyzeChange("Foo")
	if err != nil {
		t.Fatalf("opt-in AnalyzeChange(Foo): %v", err)
	}

	if len(defaultPkt.Risks) == 0 || len(optInPkt.Risks) == 0 {
		t.Fatal("expected at least one risk in both packets")
	}
	if defaultPkt.Risks[0].Score == optInPkt.Risks[0].Score {
		t.Errorf("risk score unchanged by freshness scoring: default=%f optIn=%f; want stale evidence to down-scale it",
			defaultPkt.Risks[0].Score, optInPkt.Risks[0].Score)
	}
	if optInPkt.Risks[0].Score >= defaultPkt.Risks[0].Score {
		t.Errorf("opt-in score = %f, want < default %f", optInPkt.Risks[0].Score, defaultPkt.Risks[0].Score)
	}
	// Levels must be preserved even when scores are scaled.
	if optInPkt.Risks[0].Level != defaultPkt.Risks[0].Level {
		t.Errorf("opt-in level = %q, want preserved %q", optInPkt.Risks[0].Level, defaultPkt.Risks[0].Level)
	}
}

// TestFreshnessToolSelectionSignal verifies the tool-selection freshness
// signal: fresh/no evidence yields 1.0; stale evidence yields a value < 1.0.
func TestFreshnessToolSelectionSignal(t *testing.T) {
	now := time.Now()
	bound := 24 * time.Hour

	if got := ToolFreshnessBoost("read-file", nil, now, bound); got != 1.0 {
		t.Errorf("no-evidence boost = %.2f, want 1.0", got)
	}
	if got := ToolFreshnessBoost("read-file", []domain.Evidence{{Timestamp: now.Add(-time.Hour)}}, now, bound); got != 1.0 {
		t.Errorf("fresh-evidence boost = %.2f, want 1.0", got)
	}
	stale := ToolFreshnessBoost("read-file", []domain.Evidence{{Timestamp: now.Add(-48 * time.Hour)}}, now, bound)
	if stale >= 1.0 {
		t.Errorf("stale-evidence boost = %.2f, want < 1.0", stale)
	}
}
