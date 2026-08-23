package app

import (
	"math"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/agent"
)

// Phase 17.3 — baseline-vs-Kern comparison.
//
// This file provides a deterministic baseline-vs-Kern comparison for a task,
// surfacing the spec's "baseline vs Kern" reduction percentages (input-token
// reduction, tool-call reduction, retry reduction, and cost reduction) in one
// comparable object. The baseline is a naive non-optimized agent run derived
// with the same heuristic the context-quality report uses:
//
//	raw context tokens  = kern tokens * 4
//	baseline tool calls = kern steps * 4
//	baseline retries    = 3
//	baseline cost       = baselineTokens/1000*0.01 + baselineToolCalls*0.02 + baselineRetries*0.05
//
// Everything is derived deterministically from the task's own recorded state.
// No LLM, no external agents — the "baseline agent" is a fixed heuristic so the
// percentages are reproducible.

// BaselineComparison is the Phase 17.3 baseline-vs-Kern comparison for a task.
type BaselineComparison struct {
	TaskID string `json:"task_id"`

	BaselineTokens int     `json:"baseline_tokens"` // raw-context baseline (~4x)
	KernTokens     int     `json:"kern_tokens"`
	// InputReductionPct is the % of input tokens kern saves vs the baseline.
	InputReductionPct float64 `json:"input_reduction_pct"`

	BaselineToolCalls int `json:"baseline_tool_calls"`
	KernToolCalls     int `json:"kern_tool_calls"`
	// ToolCallReductionPct is the % of tool calls kern saves vs the baseline.
	ToolCallReductionPct float64 `json:"tool_call_reduction_pct"`

	BaselineRetries int `json:"baseline_retries"`
	KernRetries     int `json:"kern_retries"`
	// RetryReductionPct is the % of retries kern avoids vs the baseline.
	RetryReductionPct float64 `json:"retry_reduction_pct"`

	EstimatedCostBaseline float64 `json:"estimated_cost_baseline"`
	EstimatedCostKern     float64 `json:"estimated_cost_kern"`
	// CostReductionPct is the % cost kern saves vs the baseline.
	CostReductionPct float64 `json:"cost_reduction_pct"`

	// VerifiedSuccess mirrors the task-outcome report's verified_success flag so
	// the comparison never implies a reduction bought at the cost of correctness.
	VerifiedSuccess bool `json:"verified_success"`
}

// CompareToBaseline computes the deterministic baseline-vs-Kern comparison for
// a task (Phase 17.3). It reuses the same heuristic baseline as the
// context-quality report so the two views are consistent.
func CompareToBaseline(t *agent.Task) BaselineComparison {
	b := BaselineComparison{TaskID: t.ID}

	kernTokens := 0
	if t.ContextPacket != nil {
		kernTokens = t.ContextPacket.TokenCount
	}
	b.KernTokens = kernTokens
	b.BaselineTokens = kernTokens * 4
	if b.BaselineTokens > 0 {
		b.InputReductionPct = (1 - float64(b.KernTokens)/float64(b.BaselineTokens)) * 100
	}

	steps := len(t.Steps)
	b.KernToolCalls = steps
	b.BaselineToolCalls = steps * 4
	if b.BaselineToolCalls > 0 {
		b.ToolCallReductionPct = (1 - float64(steps)/float64(b.BaselineToolCalls)) * 100
	}

	b.KernRetries = t.RetryCount
	b.BaselineRetries = 3
	if b.KernRetries >= b.BaselineRetries {
		b.RetryReductionPct = 0
	} else {
		b.RetryReductionPct = (1 - float64(b.KernRetries)/float64(b.BaselineRetries)) * 100
	}

	// Cost heuristic (same as the task-outcome report).
	b.EstimatedCostKern = costEstimate(kernTokens, steps, t.RetryCount)
	b.EstimatedCostBaseline = costEstimate(b.BaselineTokens, b.BaselineToolCalls, b.BaselineRetries)
	if b.EstimatedCostBaseline > 0 {
		b.CostReductionPct = (1 - b.EstimatedCostKern/b.EstimatedCostBaseline) * 100
		if b.CostReductionPct < 0 {
			b.CostReductionPct = 0
		}
	}

	b.VerifiedSuccess = outcomeSuccess(string(t.State)) && verificationPassed(t)
	return b
}

// outcomeSuccess reports whether a task state maps to a success outcome, matching
// the task-outcome report's classification.
func outcomeSuccess(state string) bool {
	return strings.Contains(state, "COMPLETED") ||
		strings.Contains(state, "READY_FOR_PR") ||
		strings.Contains(state, "PR_CREATED")
}

// costEstimate is the deterministic $-per-task estimate used by the task-outcome
// report and the baseline comparison: tokens/1000*0.01 + steps*0.02 + retries*0.05.
func costEstimate(tokens, steps, retries int) float64 {
	cost := float64(tokens)/1000*0.01 + float64(steps)*0.02 + float64(retries)*0.05
	return math.Round(cost*10000) / 10000
}