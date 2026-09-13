package index

import (
	"strings"
)

// promoteLowEdges is the finalize-time reconciliation pass for unresolved
// call edges (CG-P0-5). Extraction records a call edge's target in the form
// the source stated it ("db.Open", "Helper.doThing"), and a target whose file
// was parsed later — or whose qualifier only becomes resolvable once the
// whole symbol table exists — would otherwise render as AMBIGUOUS forever,
// because nothing ever re-resolves it. This pass runs after buildSymbolIndex
// (the completed symbol table) and before computeCallers, and reconciles
// every LOW edge:
//
//   - Resolvable now (resolveName / qualifiedCalleeSymbol against the full
//     table): the target is rewritten to the symbol's canonical FullName and
//     the edge is promoted to MEDIUM — the tier the taxonomy assigns to
//     "resolved through inference/cross-package lookup".
//   - Still unresolvable: the edge stays LOW (honest — it genuinely is a
//     phantom reference) and is counted so kern_health can surface it.
//   - Already-canonical targets (including the LOW virtual-dispatch edges
//     addDispatchEdges adds later) are untouched: they are LOW by design.
//
// HIGH/MEDIUM edge targets are NEVER rewritten: consumers key on the recorded
// form (CallersOf attribution, modernization's bridge/community analysis
// treats qualified targets as cross-package coupling), so canonicalizing
// them would silently change the graph's meaning. Only LOW edges — the
// tentative ones — are reconciled.
//
// A promoted edge's rewritten target can collide with an existing edge, so
// owners touched by the pass are re-deduped with dedupeCallEdges, which
// keeps the highest-confidence representative for each target. Counts are
// recorded on the Index and surface in kern_health.
func (ix *Index) promoteLowEdges() {
	ix.PromotedLowEdges = 0
	ix.UnresolvedLowEdges = 0
	if ix.Calls == nil {
		return
	}
	touched := map[string]bool{}
	for owner, edges := range ix.Calls {
		for i := range edges {
			e := &edges[i]
			if e.Confidence != ConfidenceLow || e.Target == "" {
				continue
			}
			if canon := ix.canonicalEdgeTarget(e.Target); canon != "" && canon != e.Target {
				e.Target = canon
				e.Confidence = ConfidenceMedium
				ix.PromotedLowEdges++
				touched[owner] = true
			} else if canon == "" {
				ix.UnresolvedLowEdges++
			}
		}
		if touched[owner] {
			ix.Calls[owner] = dedupeCallEdges(edges)
		}
	}
}

// canonicalEdgeTarget resolves a recorded edge target against the completed
// symbol table, returning the symbol's canonical FullName when it can be
// bound UNambiguously. Only DOTTED targets are considered: the qualifier
// ("db.Open" → package dir, "App.run" → receiver type) disambiguates them,
// the same logic the renderers and computeCallers use. Bare targets are left
// exactly as recorded — a bare name shared across languages or packages must
// not be bound to one arbitrary winner.
//
// Returns "" when the target genuinely does not resolve (or is ambiguous).
func (ix *Index) canonicalEdgeTarget(target string) string {
	if !strings.Contains(target, ".") {
		return ""
	}
	if d, ok := resolveName(ix, target); ok {
		return d.FullName()
	}
	if d, ok := qualifiedCalleeSymbol(ix, target); ok {
		return d.FullName()
	}
	return ""
}
