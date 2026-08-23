package app

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// TestContextQualityStaleAndDuplicateRatios verifies the Phase 17.4
// stale-context and duplicate-context ratios are derived from a context packet
// with duplicate statements and a stale source marker.
func TestContextQualityStaleAndDuplicateRatios(t *testing.T) {
	task := agent.NewTask("CODE_CHANGE", "dedup")
	_ = task.Transition(domain.TaskAnalyzing)
	_ = task.Transition(domain.TaskCompleted)
	task.ContextPacket = &domain.ContextPacket{
		TokenCount: 1000,
		Facts: []domain.Claim{
			{Statement: "x", Source: "graph", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph, Content: "g"}}},
			{Statement: "x", Source: "graph"},                                        // duplicate of the above
			{Statement: "y", Source: "superseded-cache"},                             // stale source marker
			{Statement: "z", Source: "memory", Evidence: []domain.Evidence{{Type: domain.EvidenceMemory}}},
		},
	}
	r := BuildEfficiencyReport(task)
	q := r.Quality

	if q.FactsTotal != 4 {
		t.Fatalf("facts = %d, want 4", q.FactsTotal)
	}
	if q.DuplicateContextRatio <= 0 {
		t.Errorf("duplicate ratio = %.2f, want > 0 (1 duplicate of 4 facts)", q.DuplicateContextRatio)
	}
	if q.StaleContextRatio <= 0 {
		t.Errorf("stale ratio = %.2f, want > 0 (1 stale source of 4 facts)", q.StaleContextRatio)
	}
	// Exact deterministic values: 1 dup / 4 = 25%, 1 stale / 4 = 25%.
	if q.DuplicateContextRatio != 25 {
		t.Errorf("duplicate ratio = %.2f, want 25", q.DuplicateContextRatio)
	}
	if q.StaleContextRatio != 25 {
		t.Errorf("stale ratio = %.2f, want 25", q.StaleContextRatio)
	}
}

// TestTaskOutcomeHumanInterventionAndFirstPass verifies the Phase 17.5
// first-pass-success and human-intervention flags.
func TestTaskOutcomeHumanInterventionAndFirstPass(t *testing.T) {
	// A task that retried and required an approval handoff.
	intervened := agent.NewTask("CODE_CHANGE", "approve me")
	_ = intervened.Transition(domain.TaskAnalyzing)
	_ = intervened.Transition(domain.TaskCompleted)
	intervened.RetryCount = 2
	intervened.ApprovalRef = "appr-1"
	intervened.Steps = []agent.Step{
		{Index: 1, Action: "analyze"},
		{Index: 2, Action: "approve-request"},
		{Index: 3, Action: "code"},
	}
	o := BuildEfficiencyReport(intervened).Outcome
	if o.FirstPassSuccess {
		t.Errorf("first-pass success = true, want false (retries > 0)")
	}
	if !o.HumanIntervention {
		t.Errorf("human intervention = false, want true (approve-request step + approval ref)")
	}

	// A clean, single-pass completed task.
	clean := agent.NewTask("CODE_CHANGE", "clean")
	_ = clean.Transition(domain.TaskAnalyzing)
	_ = clean.Transition(domain.TaskCompleted)
	clean.RetryCount = 0
	clean.ApprovalRef = ""
	clean.Steps = []agent.Step{{Index: 1, Action: "analyze"}, {Index: 2, Action: "code"}}
	o2 := BuildEfficiencyReport(clean).Outcome
	if !o2.FirstPassSuccess {
		t.Errorf("first-pass success = false, want true (0 retries)")
	}
	if o2.HumanIntervention {
		t.Errorf("human intervention = true, want false (no approval steps)")
	}
}

// TestTaskOutcomePostDeployRegression verifies the regression flag is set from
// a ROLLED_BACK state or a rollback artifact.
func TestTaskOutcomePostDeployRegression(t *testing.T) {
	// Rolled-back state.
	rb := agent.NewTask("CODE_CHANGE", "deploy")
	rb.State = domain.TaskRolledBack
	if got := BuildEfficiencyReport(rb).Outcome.PostDeployRegression; !got {
		t.Errorf("post-deploy regression = false, want true for ROLLED_BACK state")
	}

	// Rollback artifact.
	art := agent.NewTask("CODE_CHANGE", "deploy")
	_ = art.Transition(domain.TaskAnalyzing)
	_ = art.Transition(domain.TaskCompleted)
	art.Artifacts = []string{"reports/rollback-report.json"}
	if got := BuildEfficiencyReport(art).Outcome.PostDeployRegression; !got {
		t.Errorf("post-deploy regression = false, want true with rollback artifact")
	}
}

// TestBaselineComparisonFields verifies the Phase 17.3 baseline-vs-Kern
// comparison yields reduction percentages in the expected ranges.
func TestBaselineComparisonFields(t *testing.T) {
	task := agent.NewTask("CODE_CHANGE", "add caching")
	_ = task.Transition(domain.TaskAnalyzing)
	_ = task.Transition(domain.TaskCompleted)
	task.ContextPacket = &domain.ContextPacket{TokenCount: 1000}
	task.Steps = []agent.Step{{Index: 1, Action: "analyze"}, {Index: 2, Action: "code"}}
	task.RetryCount = 1
	task.Verification = &verification.VerificationResult{Verdict: verification.VerdictPass}

	b := CompareToBaseline(task)
	if b.BaselineTokens != 4000 || b.KernTokens != 1000 {
		t.Errorf("tokens: baseline=%d kern=%d, want 4000/1000", b.BaselineTokens, b.KernTokens)
	}
	if b.InputReductionPct != 75 {
		t.Errorf("input reduction = %.1f, want 75", b.InputReductionPct)
	}
	if b.BaselineToolCalls != 8 || b.KernToolCalls != 2 {
		t.Errorf("tool calls: baseline=%d kern=%d, want 8/2", b.BaselineToolCalls, b.KernToolCalls)
	}
	if b.ToolCallReductionPct != 75 {
		t.Errorf("tool-call reduction = %.1f, want 75", b.ToolCallReductionPct)
	}
	if b.RetryReductionPct <= 0 || b.RetryReductionPct >= 100 {
		t.Errorf("retry reduction = %.1f, want in (0,100)", b.RetryReductionPct)
	}
	if b.EstimatedCostKern >= b.EstimatedCostBaseline {
		t.Errorf("kern cost %.4f not below baseline %.4f", b.EstimatedCostKern, b.EstimatedCostBaseline)
	}
	if b.CostReductionPct <= 0 || b.CostReductionPct >= 100 {
		t.Errorf("cost reduction = %.1f, want in (0,100)", b.CostReductionPct)
	}
	if !b.VerifiedSuccess {
		t.Errorf("verified success = false, want true (completed + PASS verification)")
	}
}

// TestRenderEfficiencyIncludesNewFields verifies the CLI render surfaces the
// new stale/duplicate ratios and the baseline-vs-Kern block.
func TestRenderEfficiencyIncludesNewFields(t *testing.T) {
	task := mkTaskForEfficiency()
	task.Steps = []agent.Step{{Index: 1, Action: "analyze"}, {Index: 2, Action: "code"}}
	r := BuildEfficiencyReport(task)
	text := RenderEfficiencyReport(r)
	for _, want := range []string{"stale context", "duplicate context", "BASELINE VS KERN", "input tokens", "first-pass success"} {
		if !strings.Contains(text, want) {
			t.Errorf("render missing %q:\n%s", want, text)
		}
	}
}