package domain

import "time"

// ModelOutcomeRecord is one observed verify outcome for a (task kind, model)
// pair — the unit of cost/latency policy learning (Self-Improvement
// use-cases Tier 2 #6). The app layer records one sample per verify outcome
// at the verify choke points (kind via the deterministic agents classifier,
// model from the loop/llm configured model); the extractor aggregates records
// per (kind, model) and proposes typed-claim memories: RECOMMENDATION for the
// cheapest within-threshold model ("consider defaulting") and INFERENCE for
// below-threshold models ("review selection"). The guardrail is "learning
// proposes, provider selection approves": the output is memory only —
// nothing here changes internal/llm or provider selection.
type ModelOutcomeRecord struct {
	// Task is the task ID whose verify outcome produced this sample.
	Task string
	// Kind is the TaskKind string of the task: "code", "documentation",
	// "incident", "modernization", or "default".
	Kind string
	// Model is the model that ran the task (the loop/llm configured model).
	Model string
	// Passed reports whether verify PASSED (PASS or PASS_WITH_WARNING).
	Passed bool
	// Tokens is the token count attributed to the task, when measurable at
	// the verify site; 0 when unavailable (recorded cost is then 0 too).
	Tokens int
	// EstCost is the estimated cost of the run (Tokens * CostPerToken),
	// 0 when token counts are unavailable at the verify site.
	EstCost float64
	// At is when the verify outcome was observed.
	At time.Time
}
