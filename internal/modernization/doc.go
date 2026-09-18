// Package modernization implements the legacy modernization use case
// "Analyze this monolith and propose a safe extraction plan."
// It reuses the deterministic intelligence layer (intel.Communities,
// intel.Bridges, intel.Churn) to detect bounded contexts and generate
// phased extraction plans ordered by risk. No LLM — all analysis is
// deterministic.
// Usage:
// a := modernization.NewAnalyzer(ix) // ix *index.Index
// plan, err := a.Analyze()
// // plan.Phases[0] is the safest context to extract first
// // plan.TotalContexts reports the full context count; for large monoliths
// // the lowest-risk contexts are consolidated so Phases never exceeds 20.
package modernization
