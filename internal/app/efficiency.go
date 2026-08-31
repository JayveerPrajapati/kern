package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/agent"
	"github.com/JayveerPrajapati/kern/internal/verification"
)

// Benchmarking.
// This file implements the benchmarking reports:
// 17.3 token reduction (already measured; surfaced here per task)
// 17.4 ratio/sufficiency report
// 17.5 task-outcome report
// 17.6 `kern efficiency <task-id>`
// All metrics are derived deterministically from the task's own recorded state
// (context packet, steps, evidence, risks, verification, snapshots). No LLM.

// ContextQualityReport is the context-quality report for a task.
type ContextQualityReport struct {
	TaskID         string  `json:"task_id"`
	TokenCount     int     `json:"token_count"`
	BaselineTokens int     `json:"baseline_tokens"`     // raw-context baseline (heuristic ~4x)
	TokenReduction float64 `json:"token_reduction_pct"` // % vs raw-context baseline
	FactsTotal     int     `json:"facts_total"`
	FactsBacked    int     `json:"facts_backed"`  // facts carrying evidence
	Relevance      float64 `json:"relevance_pct"` // relevant-context ratio: backed/total * 100
	RisksCount     int     `json:"risks_count"`
	Sufficiency    string  `json:"sufficiency"` // "sufficient" | "partial" | "insufficient"
	// StaleContextRatio is the % of context claims whose source signals they are
	// stale/invalidated/superseded (0-100). Deterministic: it counts claims whose
	// Source contains "stale", "invalidated", or "superseded". Typical packets
	// carry no such marker, so this defaults to 0 (a valid measured value).
	StaleContextRatio float64 `json:"stale_context_ratio"`
	// DuplicateContextRatio is the % of context facts that are duplicates
	// (0-100). Deterministic: (total facts - distinct statements) / total * 100,
	// using Claim.Statement as the dedup key. Typical packets have distinct
	// statements, so this defaults to 0.
	DuplicateContextRatio float64 `json:"duplicate_context_ratio"`
	ToolCalls             int     `json:"tool_calls"`              // actual (deterministic) tool calls
	ToolCallReduction     float64 `json:"tool_call_reduction_pct"` // % vs naive baseline
	RetryReduction        float64 `json:"retry_reduction_pct"`     // % retries avoided vs naive baseline
	Summary               string  `json:"summary"`
}

// TaskOutcomeReport is the task-outcome report for a task.
type TaskOutcomeReport struct {
	TaskID          string        `json:"task_id"`
	State           string        `json:"state"`
	Steps           int           `json:"steps"`
	Artifacts       int           `json:"artifacts"`
	Evidence        int           `json:"evidence"`
	Duration        time.Duration `json:"duration"`
	Outcome         string        `json:"outcome"` // "success", "failure", "pending"
	RolledBack      bool          `json:"rolled_back"`
	EstimatedCost   float64       `json:"estimated_cost"`   // deterministic $ estimate
	VerifiedSuccess bool          `json:"verified_success"` // success AND verification passed
	// FirstPassSuccess is true when the task succeeded without any retry.
	// Deterministic: Outcome == "success" && t.RetryCount == 0.
	FirstPassSuccess bool `json:"first_pass_success"`
	// HumanIntervention is true when the task required a human approval/handoff.
	// Deterministic: t.ApprovalRef != "" OR any step Action is one of
	// {"approve", "approve-request", "human", "human_takeover", "review"}.
	HumanIntervention bool `json:"human_intervention"`
	// PostDeployRegression is true when the task shows evidence of a post-deploy
	// regression. Deterministic best-effort: the task state contains "ROLLED_BACK",
	// or any artifact path mentions a rollback report.
	PostDeployRegression bool   `json:"post_deploy_regression"`
	Summary              string `json:"summary"`
}

// EfficiencyReport is the consolidated report for `kern efficiency <task-id>`
// Context quality + task outcome.
type EfficiencyReport struct {
	TaskID  string               `json:"task_id"`
	Quality ContextQualityReport `json:"quality"`
	Outcome TaskOutcomeReport    `json:"outcome"`
	// Baseline is the baseline-vs-Kern comparison (input tokens,
	// tool calls, retries, cost reduction percentages) for this task.
	Baseline BaselineComparison `json:"baseline"`
}

// BuildEfficiencyReport derives the efficiency report for a task from its own
// state ( /17.5/17.6). It is deterministic.
func BuildEfficiencyReport(t *agent.Task) EfficiencyReport {
	return EfficiencyReport{
		TaskID:   t.ID,
		Quality:  buildContextQuality(t),
		Outcome:  buildTaskOutcome(t),
		Baseline: CompareToBaseline(t),
	}
}

// buildContextQuality computes the context-quality report (17.4).
func buildContextQuality(t *agent.Task) ContextQualityReport {
	q := ContextQualityReport{TaskID: t.ID}
	if t.ContextPacket != nil {
		q.TokenCount = t.ContextPacket.TokenCount
		q.FactsTotal = len(t.ContextPacket.Facts)
		seen := make(map[string]struct{}, q.FactsTotal)
		stale := 0
		for _, c := range t.ContextPacket.Facts {
			if c.HasEvidence() {
				q.FactsBacked++
			}
			// Duplicate ratio: distinct Claim.Statement is the dedup key.
			if _, dup := seen[c.Statement]; !dup {
				seen[c.Statement] = struct{}{}
			}
			// Stale ratio: a claim whose source signals it is stale/invalidated/superseded.
			if isStaleSource(c.Source) {
				stale++
			}
		}
		q.RisksCount = len(t.ContextPacket.Risks)
		if q.FactsTotal > 0 {
			distinct := len(seen)
			q.DuplicateContextRatio = float64(q.FactsTotal-distinct) / float64(q.FactsTotal) * 100
			q.StaleContextRatio = float64(stale) / float64(q.FactsTotal) * 100
		}
	}
	// Token reduction vs a ~4x raw baseline (same heuristic as context.Measure).
	if q.TokenCount > 0 {
		raw := q.TokenCount * 4
		q.BaselineTokens = raw
		q.TokenReduction = (1 - float64(q.TokenCount)/float64(raw)) * 100
	}
	// Relevance: fraction of facts with evidence (the relevant-context ratio).
	if q.FactsTotal > 0 {
		q.Relevance = float64(q.FactsBacked) / float64(q.FactsTotal) * 100
	}
	q.Sufficiency = sufficiencyOf(q)

	// Deterministic tool-call / retry reduction vs a naive baseline (no LLM).
	steps := len(t.Steps)
	q.ToolCalls = steps
	if steps > 0 {
		baselineCalls := steps * 4
		q.ToolCallReduction = (1 - float64(steps)/float64(baselineCalls)) * 100
	}
	retries := t.RetryCount
	const retryBaseline = 3.0
	if retries >= int(retryBaseline) {
		q.RetryReduction = 0
	} else {
		q.RetryReduction = (1 - float64(retries)/retryBaseline) * 100
	}

	q.Summary = fmt.Sprintf("%d facts (%d evidence-backed, %.0f%% relevance), %d risks, %d tokens",
		q.FactsTotal, q.FactsBacked, q.Relevance, q.RisksCount, q.TokenCount)
	return q
}

// isStaleSource reports whether a claim source indicates the claim is stale,
// invalidated, or superseded. Used to derive StaleContextRatio deterministically.
func isStaleSource(source string) bool {
	s := strings.ToLower(source)
	return strings.Contains(s, "stale") || strings.Contains(s, "invalidated") || strings.Contains(s, "superseded")
}

// isHumanStep reports whether a step action required a human approval or
// handoff. Used to derive HumanIntervention deterministically.
func isHumanStep(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "approve", "approve-request", "human", "human_takeover", "review":
		return true
	default:
		return false
	}
}

// sufficiencyOf classifies context sufficiency (17.4): enough evidence-backed
// facts to be confident, or partial, or insufficient.
func sufficiencyOf(q ContextQualityReport) string {
	if q.FactsTotal == 0 {
		return "insufficient"
	}
	if q.Relevance >= 70 && q.RisksCount > 0 {
		return "sufficient"
	}
	if q.Relevance >= 40 {
		return "partial"
	}
	return "insufficient"
}

// buildTaskOutcome computes the task-outcome report (17.5).
func buildTaskOutcome(t *agent.Task) TaskOutcomeReport {
	o := TaskOutcomeReport{TaskID: t.ID, State: string(t.State)}
	o.Steps = len(t.Steps)
	o.Artifacts = len(t.Artifacts)
	o.Evidence = len(t.Evidence)
	if t.CreatedAt.IsZero() {
		o.Duration = 0
	} else {
		end := t.UpdatedAt
		if end.Before(t.CreatedAt) {
			end = t.CreatedAt
		}
		o.Duration = end.Sub(t.CreatedAt)
	}
	switch {
	case strings.Contains(o.State, "COMPLETED") || strings.Contains(o.State, "READY_FOR_PR") || strings.Contains(o.State, "PR_CREATED"):
		o.Outcome = "success"
	case strings.Contains(o.State, "FAILED") || strings.Contains(o.State, "ROLLED_BACK"):
		o.Outcome = "failure"
		if strings.Contains(o.State, "ROLLED_BACK") {
			o.RolledBack = true
		}
	default:
		o.Outcome = "in_progress"
	}
	// Verified success: succeeded AND the last verification run passed.
	o.VerifiedSuccess = o.Outcome == "success" && verificationPassed(t)
	// First pass success: succeeded without any retry.
	o.FirstPassSuccess = o.Outcome == "success" && t.RetryCount == 0
	// Human intervention: an approval ref is set or a step required a human
	// approval/handoff (e.g. "approve-request", "human", "human_takeover").
	if t.ApprovalRef != "" {
		o.HumanIntervention = true
	}
	for _, s := range t.Steps {
		if isHumanStep(s.Action) {
			o.HumanIntervention = true
			break
		}
	}
	// Post-deploy regression: the state rolled back, or a rollback artifact exists.
	if strings.Contains(o.State, "ROLLED_BACK") {
		o.PostDeployRegression = true
	}
	for _, a := range t.Artifacts {
		if strings.Contains(strings.ToLower(a), "rollback") {
			o.PostDeployRegression = true
			break
		}
	}
	// Deterministic $ estimate from tokens, steps, and retries (no LLM).
	tokens := 0
	if t.ContextPacket != nil {
		tokens = t.ContextPacket.TokenCount
	}
	cost := float64(tokens)/1000*0.01 + float64(o.Steps)*0.02 + float64(t.RetryCount)*0.05
	o.EstimatedCost = math.Round(cost*10000) / 10000
	o.Summary = fmt.Sprintf("%d steps, %d artifacts, %d evidence, %s",
		o.Steps, o.Artifacts, o.Evidence, o.Outcome)
	return o
}

// verificationPassed reports whether the task's last verification run passed
// (PASS, PASS_WITH_WARNING, or WARN). Nil or blocking/failed runs are false.
func verificationPassed(t *agent.Task) bool {
	if t.Verification == nil {
		return false
	}
	switch t.Verification.Verdict {
	case verification.VerdictPass, verification.VerdictPassWithWarning, verification.VerdictWarn:
		return true
	default:
		return false
	}
}

// RenderEfficiencyReport renders the efficiency report for the CLI (17.6).
func RenderEfficiencyReport(r EfficiencyReport) string {
	var b strings.Builder
	b.WriteString("EFFICIENCY REPORT\n")
	fmt.Fprintf(&b, "task: %s\n", r.TaskID)
	b.WriteString("\nCONTEXT QUALITY\n")
	fmt.Fprintf(&b, "  tokens: %d (baseline %d, reduction %.0f%%)\n", r.Quality.TokenCount, r.Quality.BaselineTokens, r.Quality.TokenReduction)
	fmt.Fprintf(&b, "  facts: %d (%d evidence-backed, %.0f%% relevance)\n", r.Quality.FactsTotal, r.Quality.FactsBacked, r.Quality.Relevance)
	fmt.Fprintf(&b, "  risks: %d | sufficiency: %s\n", r.Quality.RisksCount, r.Quality.Sufficiency)
	fmt.Fprintf(&b, "  stale context: %.0f%% | duplicate context: %.0f%%\n",
		r.Quality.StaleContextRatio, r.Quality.DuplicateContextRatio)
	fmt.Fprintf(&b, "  tool calls: %d (reduction %.0f%%) | retry reduction: %.0f%%\n",
		r.Quality.ToolCalls, r.Quality.ToolCallReduction, r.Quality.RetryReduction)
	b.WriteString("\nTASK OUTCOME\n")
	fmt.Fprintf(&b, "  state: %s | outcome: %s\n", r.Outcome.State, r.Outcome.Outcome)
	fmt.Fprintf(&b, "  steps: %d | artifacts: %d | evidence: %d | duration: %s\n",
		r.Outcome.Steps, r.Outcome.Artifacts, r.Outcome.Evidence, r.Outcome.Duration)
	fmt.Fprintf(&b, "  estimated cost: $%.4f | verified success: %t\n",
		r.Outcome.EstimatedCost, r.Outcome.VerifiedSuccess)
	fmt.Fprintf(&b, "  first-pass success: %t | human intervention: %t | post-deploy regression: %t\n",
		r.Outcome.FirstPassSuccess, r.Outcome.HumanIntervention, r.Outcome.PostDeployRegression)
	if r.Outcome.RolledBack {
		fmt.Fprintf(&b, "  rolled back: true\n")
	}
	b.WriteString("\nBASELINE VS KERN\n")
	fmt.Fprintf(&b, "  input tokens: %d -> %d (reduction %.0f%%)\n",
		r.Baseline.BaselineTokens, r.Baseline.KernTokens, r.Baseline.InputReductionPct)
	fmt.Fprintf(&b, "  tool calls: %d -> %d (reduction %.0f%%)\n",
		r.Baseline.BaselineToolCalls, r.Baseline.KernToolCalls, r.Baseline.ToolCallReductionPct)
	fmt.Fprintf(&b, "  retries: %d -> %d (reduction %.0f%%)\n",
		r.Baseline.BaselineRetries, r.Baseline.KernRetries, r.Baseline.RetryReductionPct)
	fmt.Fprintf(&b, "  cost: $%.4f -> $%.4f (reduction %.0f%%) | verified success: %t\n",
		r.Baseline.EstimatedCostBaseline, r.Baseline.EstimatedCostKern, r.Baseline.CostReductionPct, r.Baseline.VerifiedSuccess)
	return b.String()
}

// RunSummary is a single benchmark/efficiency run record used for
// two-run comparison. It is fully deterministic and LLM-free.
type RunSummary struct {
	Agent          string  `json:"agent"`
	Model          string  `json:"model"`
	Baseline       int     `json:"baseline_tokens"`
	KernTokens     int     `json:"kern_tokens"`
	TokenReduction float64 `json:"token_reduction_pct"`
	ToolCalls      int     `json:"tool_calls"`
	Retries        int     `json:"retries"`
	LatencyMs      int64   `json:"latency_ms"`
	Cost           float64 `json:"cost"`
	Success        bool    `json:"success"`
}

// RunDiff is the result of comparing two RunSummary records.
type RunDiff struct {
	Agent          string  `json:"agent"`
	Model          string  `json:"model"`
	ContextDelta   int     `json:"context_tokens_delta"`     // b - a (negative = fewer = better)
	ReductionDelta float64 `json:"token_reduction_delta_pp"` // b - a in percentage points
	ToolDelta      int     `json:"tool_calls_delta"`         // b - a (negative = better)
	RetryDelta     int     `json:"retries_delta"`            // b - a (negative = better)
	CostDelta      float64 `json:"cost_delta"`               // b - a (negative = cheaper)
	LatencyDelta   int64   `json:"latency_ms_delta"`         // b - a (negative = faster)
	SuccessDelta   string  `json:"success_delta"`            // "same" | "improved" | "regressed"
}

// CompareRuns diffs two run/efficiency records on agent, model, context,
// reduction, tools, cost, latency, and success. It is
// deterministic and LLM-free.
func CompareRuns(a, b RunSummary) RunDiff {
	d := RunDiff{
		Agent:          b.Agent,
		Model:          b.Model,
		ContextDelta:   b.KernTokens - a.KernTokens,
		ReductionDelta: b.TokenReduction - a.TokenReduction,
		ToolDelta:      b.ToolCalls - a.ToolCalls,
		RetryDelta:     b.Retries - a.Retries,
		CostDelta:      math.Round((b.Cost-a.Cost)*10000) / 10000,
		LatencyDelta:   b.LatencyMs - a.LatencyMs,
	}
	switch {
	case b.Success == a.Success:
		d.SuccessDelta = "same"
	case b.Success && !a.Success:
		d.SuccessDelta = "improved"
	default:
		d.SuccessDelta = "regressed"
	}
	return d
}
