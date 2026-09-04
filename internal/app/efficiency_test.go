package app

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

func mkTaskForEfficiency() *agent.Task {
	t := agent.NewTask("CODE_CHANGE", "add caching")
	_ = t.Transition(domain.TaskAnalyzing)
	_ = t.Transition(domain.TaskCompleted)
	t.ContextPacket = &domain.ContextPacket{
		TokenCount: 1000,
		Facts: []domain.Claim{
			{Statement: "a", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph, Content: "g"}}},
			{Statement: "b", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph, Content: "g"}}},
			{Statement: "c"}, // no evidence
		},
		Risks: []domain.Risk{{Level: domain.RiskMedium}},
	}
	t.Evidence = []domain.Claim{{Statement: "e"}}
	return t
}

// TestEfficiencyReportQuality verifies the context-quality report (17.4):
// sufficiency, token reduction, relevance.
func TestEfficiencyReportQuality(t *testing.T) {
	r := BuildEfficiencyReport(mkTaskForEfficiency())
	q := r.Quality
	if q.FactsTotal != 3 || q.FactsBacked != 2 {
		t.Errorf("facts = %d backed %d, want 3/2", q.FactsTotal, q.FactsBacked)
	}
	if q.TokenReduction != 75 {
		t.Errorf("token reduction = %.1f, want 75", q.TokenReduction)
	}
	if q.Sufficiency != "partial" {
		t.Errorf("sufficiency = %q, want partial (2/3 backed = 67%% relevance)", q.Sufficiency)
	}
	if q.RisksCount != 1 {
		t.Errorf("risks = %d, want 1", q.RisksCount)
	}
}

func TestEfficiencyReportOutcome(t *testing.T) {
	r := BuildEfficiencyReport(mkTaskForEfficiency())
	if r.Outcome.Outcome != "success" {
		t.Errorf("outcome = %q, want success", r.Outcome.Outcome)
	}
	if r.Outcome.State == "" || r.Outcome.Steps < 0 {
		t.Errorf("outcome fields missing: %+v", r.Outcome)
	}
}

func TestRenderEfficiencyReport(t *testing.T) {
	r := BuildEfficiencyReport(mkTaskForEfficiency())
	text := RenderEfficiencyReport(r)
	for _, want := range []string{"EFFICIENCY", "CONTEXT QUALITY", "TASK OUTCOME", "sufficiency"} {
		if !strings.Contains(text, want) {
			t.Errorf("render missing %q:\n%s", want, text)
		}
	}
}

// TestEfficiencyDuration verifies the outcome duration is computed from the
// task timestamps.
func TestEfficiencyDuration(t *testing.T) {
	task := mkTaskForEfficiency()
	now := time.Now()
	task.CreatedAt = now.Add(-time.Minute)
	task.UpdatedAt = now
	r := BuildEfficiencyReport(task)
	if r.Outcome.Duration < time.Second {
		t.Errorf("duration = %v, want >= 1s", r.Outcome.Duration)
	}
}

func TestEfficiencyReportInsufficient(t *testing.T) {
	task := agent.NewTask("CODE_CHANGE", "empty")
	_ = task.Transition(domain.TaskFailed)
	task.ContextPacket = &domain.ContextPacket{TokenCount: 500} // no facts
	r := BuildEfficiencyReport(task)
	if r.Quality.Sufficiency != "insufficient" {
		t.Errorf("sufficiency = %q, want insufficient", r.Quality.Sufficiency)
	}
}

// TestEfficiencyReportExtendedFields verifies the extended report
// fields: baseline tokens, tool-call reduction, retry reduction, cost, and
// verified success are all deterministically derived from task state.
func TestEfficiencyReportExtendedFields(t *testing.T) {
	task := mkTaskForEfficiency()
	task.RetryCount = 1
	task.Steps = []agent.Step{{Index: 1, Action: "analyze"}, {Index: 2, Action: "code"}}
	r := BuildEfficiencyReport(task)

	if r.Quality.BaselineTokens != 4000 {
		t.Errorf("baseline tokens = %d, want 4000 (4x of 1000)", r.Quality.BaselineTokens)
	}
	if r.Quality.ToolCalls != 2 {
		t.Errorf("tool calls = %d, want 2", r.Quality.ToolCalls)
	}
	if r.Quality.ToolCallReduction != 75 {
		t.Errorf("tool-call reduction = %.1f, want 75", r.Quality.ToolCallReduction)
	}
	if r.Quality.RetryReduction != 100-(100.0/3.0*1.0) {
		t.Errorf("retry reduction = %.1f, want ~66.7", r.Quality.RetryReduction)
	}
	if r.Outcome.EstimatedCost <= 0 {
		t.Errorf("estimated cost = %.4f, want > 0", r.Outcome.EstimatedCost)
	}
}

// TestVerifiedSuccess verifies verified_success requires both a success outcome
// and a passing verification run.
func TestVerifiedSuccess(t *testing.T) {
	ok := mkTaskForEfficiency()
	ok.Verification = &verification.VerificationResult{Verdict: verification.VerdictPass}
	if got := BuildEfficiencyReport(ok).Outcome.VerifiedSuccess; !got {
		t.Errorf("verified success = false, want true")
	}

	noV := mkTaskForEfficiency()
	noV.Verification = nil
	if got := BuildEfficiencyReport(noV).Outcome.VerifiedSuccess; got {
		t.Errorf("verified success with nil verification = true, want false")
	}

	fail := mkTaskForEfficiency()
	fail.Verification = &verification.VerificationResult{Verdict: verification.VerdictFail}
	if got := BuildEfficiencyReport(fail).Outcome.VerifiedSuccess; got {
		t.Errorf("verified success with FAIL verdict = true, want false")
	}
}

// TestCompareRuns verifies CompareRuns diffs two fabricated runs on context,
// tools, retries, cost, latency, and success .
func TestCompareRuns(t *testing.T) {
	a := RunSummary{Agent: "ag", Model: "m1", KernTokens: 1000, TokenReduction: 75, ToolCalls: 4, Retries: 2, Cost: 0.5, LatencyMs: 1200, Success: false}
	b := RunSummary{Agent: "ag", Model: "m2", KernTokens: 500, TokenReduction: 90, ToolCalls: 2, Retries: 0, Cost: 0.25, LatencyMs: 600, Success: true}

	d := CompareRuns(a, b)
	if d.ContextDelta != -500 {
		t.Errorf("context delta = %d, want -500", d.ContextDelta)
	}
	if d.ReductionDelta != 15 {
		t.Errorf("reduction delta = %.1f, want 15", d.ReductionDelta)
	}
	if d.ToolDelta != -2 {
		t.Errorf("tool delta = %d, want -2", d.ToolDelta)
	}
	if d.RetryDelta != -2 {
		t.Errorf("retry delta = %d, want -2", d.RetryDelta)
	}
	if d.CostDelta != -0.25 {
		t.Errorf("cost delta = %.4f, want -0.25", d.CostDelta)
	}
	if d.LatencyDelta != -600 {
		t.Errorf("latency delta = %d, want -600", d.LatencyDelta)
	}
	if d.SuccessDelta != "improved" {
		t.Errorf("success delta = %q, want improved", d.SuccessDelta)
	}
}
