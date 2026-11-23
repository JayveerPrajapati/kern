package intel

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ContextSummary is the count-only description of one symbol that
// `kern context` renders in concise mode. It is built WITHOUT the evidence
// slice (ix.Context) or a full explore report: only the entry resolver, the
// existing graph accessors and the existing blast-radius walk run. Every
// count is defined to match the concise renderer (RenderExploreConcise) for
// the same symbol, so the fast path cannot drift from ExploreBudgeted.
type ContextSummary struct {
	Symbol     string
	Resolved   string
	Definition index.Symbol
	Callers    int
	Callees    int
	Blast      int
	Files      int
	Risk       string
}

// contextSummaryFuzzyScore is the strong-fuzzy threshold the entry resolver
// (ResolveFuzzy) uses for an every-query-word match. Ambiguity is judged with
// the same bar so the fast path refuses exactly the queries the CLI treats as
// ambiguous and hands them to the full did-you-mean path.
const contextSummaryFuzzyScore = 150

// ContextSummaryFor resolves symbol and counts its callers, callees and
// blast radius without building a source/evidence slice. It mirrors the
// exact parameters conciseContext hands to ExploreBudgeted
// (DefaultExploreBounds(-1, 0) => depth 2, 30 radius nodes; minConf "";
// no token budget) and the caller/callee dedupe rules that build the report
// lists, so its counts equal ExploreBudgeted's for the same symbol.
//
// It fails closed (returns an error) on a nil index, an empty symbol, an
// ambiguous fuzzy query, a resolution failure, or a missing definition; the
// caller then falls back to the existing full-context path. The graph
// accessors (CallersFor/CallsFor/blastRadiusWalk) are total — they have no
// error channel — so a resolved definition is the only graph precondition.
func ContextSummaryFor(ix *index.Index, symbol string) (*ContextSummary, error) {
	if ix == nil {
		return nil, fmt.Errorf("context summary: index is nil")
	}
	if strings.TrimSpace(symbol) == "" {
		return nil, fmt.Errorf("context summary: symbol is required")
	}
	if ambiguousQuery(ix, symbol) {
		return nil, fmt.Errorf("context summary: ambiguous symbol: %s", symbol)
	}
	resolved, d, ok := ResolveEntry(ix, symbol)
	if !ok || d == nil {
		return nil, fmt.Errorf("context summary: cannot resolve %s", symbol)
	}

	// Mirror conciseContext's ExploreBudgeted bounds exactly.
	depth, maxNodes := DefaultExploreBounds(-1, 0)

	// Direct callers: ExploreBudgeted appends one row per DISTINCT simple
	// name (a call edge may record the same callee under several qualified
	// forms). The confidence filter is empty here (minConf ""), so every
	// edge passes.
	callers := 0
	seenCallers := map[string]bool{}
	for _, c := range ix.CallersFor(*d) {
		name := simpleName(c)
		if seenCallers[name] {
			continue
		}
		seenCallers[name] = true
		callers++
	}

	// Direct callees: same distinct-simple-name rule as the caller list.
	callees := 0
	seenCallees := map[string]bool{}
	for _, c := range ix.CallsFor(*d) {
		name := simpleName(c)
		if seenCallees[name] {
			continue
		}
		seenCallees[name] = true
		callees++
	}

	// The existing blast-radius walk, with the same root/strict/maxNodes and
	// the same depth cap ExploreBudgeted applies to its report.
	radius, radiusSyms, dist, _ := blastRadiusWalk(ix, []string{resolved}, false, maxNodes, *d)
	if depth > 0 {
		var capped []string
		var cappedSyms []index.Symbol
		for i, s := range radius {
			if dist[s] <= depth {
				capped = append(capped, s)
				cappedSyms = append(cappedSyms, radiusSyms[i])
			}
		}
		radius, radiusSyms = capped, cappedSyms
	}

	// Files come from the resolved blast SYMBOLS (not a name->file map), the
	// same identity-keyed attribution the explore renderer uses.
	files := len(affectedFilesOf(radiusSyms))

	return &ContextSummary{
		Symbol:     symbol,
		Resolved:   resolved,
		Definition: *d,
		Callers:    callers,
		Callees:    callees,
		Blast:      len(radius),
		Files:      files,
		Risk:       conciseRisk(callers, len(radius)),
	}, nil
}

// ambiguousQuery reports whether a bare query has more than one distinct
// strong fuzzy candidate. Exact names and qualified queries are never
// ambiguous here: the entry resolver owns (and deterministically applies)
// their package-qualifier / production-preference policy. This mirrors the
// CLI's what-if ambiguity gate so the fast path refuses exactly those
// queries and leaves the did-you-mean behavior to the full path.
func ambiguousQuery(ix *index.Index, symbol string) bool {
	if strings.ContainsAny(symbol, "./") {
		return false
	}
	if _, ok := ix.FindSymbol(symbol); ok {
		return false
	}
	if _, ok := ix.ResolveName(symbol); ok {
		return false
	}
	seen := map[string]bool{}
	strong := 0
	for _, h := range RankedSearchScored(ix, symbol, 5) {
		if !h.MatchedAll || h.Score < contextSummaryFuzzyScore {
			continue
		}
		name := h.Symbol.FullName()
		if name == "" {
			name = h.Symbol.Name
		}
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		strong++
	}
	return strong > 1
}
