package metrics

import (
	"testing"
	"time"
)

func TestRecorderPerformance(t *testing.T) {
	r := New()
	r.RecordIndexBuild(100 * time.Millisecond)
	r.RecordIndexBuild(200 * time.Millisecond)
	r.RecordGraphQuery(5 * time.Millisecond)

	s := r.Snapshot()
	if s.IndexBuildCount != 2 {
		t.Errorf("IndexBuildCount = %d, want 2", s.IndexBuildCount)
	}
	if s.IndexBuildAvgMs != 150 {
		t.Errorf("IndexBuildAvgMs = %.1f, want 150", s.IndexBuildAvgMs)
	}
	if s.GraphQueryAvgMs != 5 {
		t.Errorf("GraphQueryAvgMs = %.1f, want 5", s.GraphQueryAvgMs)
	}
}

func TestRecorderCacheHitRate(t *testing.T) {
	r := New()
	r.RecordCacheHit()
	r.RecordCacheHit()
	r.RecordCacheMiss()

	s := r.Snapshot()
	if s.CacheHitRate != 2.0/3.0 {
		t.Errorf("CacheHitRate = %.2f, want %.2f", s.CacheHitRate, 2.0/3.0)
	}
}

func TestRecorderTokenReduction(t *testing.T) {
	r := New()
	r.RecordTokenUsage(1000, 250) // 75% reduction

	s := r.Snapshot()
	if s.TokenReductionPct != 75 {
		t.Errorf("TokenReductionPct = %.1f, want 75", s.TokenReductionPct)
	}
}

func TestRecorderImpactAccuracy(t *testing.T) {
	r := New()
	r.RecordImpactPrediction(true)
	r.RecordImpactPrediction(true)
	r.RecordImpactPrediction(false)

	s := r.Snapshot()
	if delta := abs(s.ImpactAccuracyPct - (2.0/3.0)*100); delta > 0.01 {
		t.Errorf("ImpactAccuracyPct = %.4f, want ~66.6667", s.ImpactAccuracyPct)
	}
}

func TestRecorderSelfObservability(t *testing.T) {
	r := New()
	r.RecordRequest()
	r.RecordRequest()
	r.RecordAgentRun()
	r.RecordError()

	s := r.Snapshot()
	if s.RequestCount != 2 {
		t.Error("RequestCount")
	}
	if s.AgentRunCount != 1 {
		t.Error("AgentRunCount")
	}
	if s.ErrorCount != 1 {
		t.Error("ErrorCount")
	}
}

func TestRecorderGovernance(t *testing.T) {
	r := New()
	gd := GovernanceData{
		AgentCount:      5,
		TaskCount:       12,
		BlocksCount:     2,
		OverridesCount:  1,
		ViolationsCount: 3,
		AvgConfidence:   0.85,
	}
	s := r.SnapshotWithGovernance(gd)
	if s.AgentCount != 5 {
		t.Error("AgentCount")
	}
	if s.AvgConfidence != 0.85 {
		t.Error("AvgConfidence")
	}
}

func TestNilRecorderIsSafe(t *testing.T) {
	var r *Recorder                 // nil
	r.RecordIndexBuild(time.Second) // should not panic
	r.RecordRequest()               // should not panic
	s := r.Snapshot()               // should not panic
	if s.Timestamp.IsZero() {
		t.Error("nil Snapshot should still have timestamp")
	}
}

func TestReport(t *testing.T) {
	r := New()
	r.RecordTokenUsage(1000, 300)
	s := r.Snapshot()
	report := s.Report()
	if report == "" {
		t.Error("empty report")
	}
	if !contains(report, "Token reduction") {
		t.Error("report missing token reduction")
	}
	if !contains(report, "Performance") {
		t.Error("report missing performance section")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func TestDefaultIsSingleton(t *testing.T) {
	a := Default()
	b := Default()
	if a == nil {
		t.Fatal("Default() returned nil")
	}
	if a != b {
		t.Error("Default() returned different instances on consecutive calls")
	}
}

func TestRenderContainsAllSections(t *testing.T) {
	r := New()
	r.RecordIndexBuild(100 * time.Millisecond)
	r.RecordGraphQuery(5 * time.Millisecond)
	r.RecordRequest()
	r.RecordTokenUsage(1000, 250)

	out := r.Render()
	for _, want := range []string{
		"performance (F-46)",
		"self-observability (F-47)",
		"product success (F-56)",
		"governance (F-41)",
	} {
		if !contains(out, want) {
			t.Errorf("Render() output missing section %q\n%s", want, out)
		}
	}
}

func TestRenderNilSafe(t *testing.T) {
	var r *Recorder // nil
	if out := r.Render(); out != "" {
		t.Errorf("nil Render() = %q, want empty string", out)
	}
}

func TestResetClearsAll(t *testing.T) {
	r := New()
	r.RecordIndexBuild(100 * time.Millisecond)
	r.RecordGraphQuery(5 * time.Millisecond)
	r.RecordContextRetrieval(10 * time.Millisecond)
	r.RecordMemoryRecall(3 * time.Millisecond)
	r.RecordPolicyEval(2 * time.Millisecond)
	r.RecordToolCall(7 * time.Millisecond)
	r.RecordVerification(4 * time.Millisecond)
	r.RecordCacheHit()
	r.RecordCacheMiss()
	r.RecordRequest()
	r.RecordAgentRun()
	r.RecordLLMLatency(50 * time.Millisecond)
	r.RecordIndexing()
	r.RecordApproval()
	r.RecordSandboxOp()
	r.RecordIncident()
	r.RecordError()
	r.RecordTokenUsage(1000, 250)
	r.RecordAnalysis()
	r.RecordFalsePositive()
	r.RecordImpactPrediction(true)
	r.RecordImpactPrediction(false)

	// Sanity: snapshot is populated before reset.
	pre := r.Snapshot()
	if pre.IndexBuildCount != 1 || pre.RequestCount != 1 || pre.AnalysisCount != 1 {
		t.Fatalf("pre-reset snapshot not populated: %+v", pre)
	}

	r.Reset()

	s := r.Snapshot()
	if s.IndexBuildCount != 0 || s.IndexBuildAvgMs != 0 || s.TotalIndexTimeMs != 0 {
		t.Errorf("performance not cleared: %+v", s)
	}
	if s.GraphQueryAvgMs != 0 || s.ContextRetrievalAvgMs != 0 || s.MemoryRecallAvgMs != 0 ||
		s.PolicyEvalAvgMs != 0 || s.ToolCallAvgMs != 0 || s.VerificationAvgMs != 0 {
		t.Errorf("duration averages not cleared: %+v", s)
	}
	if s.CacheHitRate != 0 {
		t.Errorf("CacheHitRate = %v, want 0", s.CacheHitRate)
	}
	if s.RequestCount != 0 || s.ToolCallCount != 0 || s.AgentRunCount != 0 ||
		s.LLMLatencyAvgMs != 0 || s.IndexingCount != 0 || s.ApprovalCount != 0 ||
		s.SandboxOps != 0 || s.IncidentCount != 0 || s.ErrorCount != 0 {
		t.Errorf("self-observability not cleared: %+v", s)
	}
	if s.TokenReductionPct != 0 || s.AnalysisCount != 0 || s.FalsePositiveRate != 0 ||
		s.ImpactAccuracyPct != 0 {
		t.Errorf("product success not cleared: %+v", s)
	}
}
