// Per-tool cost metadata (Rec P2-9).
//
// Agents budget their context from tool output size and wall-clock cost.
// This file carries a deterministic per-tool cost hint — estimated latency
// and output-token volume — surfaced on the kern_meta classified line and
// documented in `kern guide`. It is a HINT (tiered, derived from the
// measured profile: exec/verify-class tools are slow, index-backed
// search/graph tools are medium, everything else fast), never a promise:
// per-tool latency actuals live in the metrics recorder (RecordToolCall)
// and kern_stats reports token savings. Deterministic and offline — no LLM,
// no network, no catalog-shape change.
package mcp

// CostHint is a deterministic latency/output estimate for one tool.
type CostHint struct {
	EstMs     int // estimated wall-clock latency
	EstTokens int // estimated output tokens (24KiB cap ≈ 6k tokens)
}

var (
	costFast   = CostHint{EstMs: 10, EstTokens: 150}
	costMedium = CostHint{EstMs: 150, EstTokens: 1200}
	costSlow   = CostHint{EstMs: 800, EstTokens: 4000}
)

// slowCostTools: exec/verify-class tools that can take seconds to minutes.
// Mirrors the slowTools progress set after the 2026-10-18 surface slim.
var slowCostTools = map[string]bool{
	"kern_exec":   true,
	"kern_heal":   true,
	"kern_verify": true,
	"kern_run":    true,
	"kern_doc":    true,
}

// mediumCostTools: index-backed search/graph/context/retrieval tools that
// touch the symbol index but return bounded results.
var mediumCostTools = map[string]bool{
	"kern_search": true, "kern_explore": true, "kern_context": true,
	"kern_graph": true, "kern_probe": true,
	"kern_impact": true, "kern_arch": true, "kern_project_map": true,
	"kern_optimize": true, "kern_why": true,
	"kern_path": true,
}

// costHintFor returns the deterministic cost hint for a tool. Unknown tools
// default to the fast tier (the majority of the catalog is fast read tools).
func costHintFor(tool string) CostHint {
	if slowCostTools[tool] {
		return costSlow
	}
	if mediumCostTools[tool] {
		return costMedium
	}
	return costFast
}
