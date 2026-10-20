package index

import (
	"github.com/JayveerPrajapati/kern/internal/langparse"
)

func (ix *Index) CallBindings() GraphResult {
	graph := GraphResult{SchemaVersion: GraphSchemaVersion}
	catalog := newExportCatalog(ix)
	nodes := map[string]GraphNode{}
	for _, call := range catalog.calls(nil) {
		nodes[call.from.ID] = call.from
		nodes[call.to.ID] = call.to
		graph.Edges = append(graph.Edges, call.edge)
		if call.edge.Resolution != "resolved" {
			graph.UnresolvedCalls++
		}
	}
	for _, node := range nodes {
		graph.Nodes = append(graph.Nodes, node)
	}
	graph.sort()
	return graph
}

func (ix *Index) CallersFor(symbol Symbol) []string {
	catalog := newExportCatalog(ix)
	identity := symbol.DeclarationID()
	var callers []string
	// Single-root endpoint iteration, NOT catalog.calls([]Symbol{symbol}):
	// calls() builds a positions set + sorted slice + a fresh []exportCall
	// copy per query — the blast-radius walk (intel.blastRadiusWalk) calls
	// CallersFor once per visited node, so that per-node allocation was a
	// measured latency regression on large repos. byEndpoint positions are
	// already appended in catalog.records order (ascending), so iterating
	// them directly is equivalent and allocation-free.
	for _, position := range catalog.byEndpoint[identity] {
		call := catalog.records[position]
		if (call.edge.SourceFile != "" || call.edge.Synth != "") && call.to.ID == identity && call.from.ID != identity {
			callers = append(callers, call.from.Name)
		}
	}
	for _, caller := range ix.callersForLegacy(symbol) {
		legacy := len(ix.Calls) == 0
		for _, edge := range ix.Calls[caller] {
			if edge.SourceFile == "" && edge.Synth == "" && !edge.Dynamic && langparse.BaseName(edge.Target) == symbol.Name {
				legacy = true
			}
		}
		if legacy {
			callers = append(callers, caller)
		}
	}
	return dedupeSorted(callers)
}

func (ix *Index) CallerDeclarations(caller string, target Symbol) ([]Symbol, bool) {
	catalog := newExportCatalog(ix)
	identity := target.DeclarationID()
	seen := map[string]bool{}
	var declarations []Symbol
	opaque := false
	// Single-root endpoint iteration — same allocation-free rationale as
	// CallersFor: this runs once per (caller, target) pair in the
	// blast-radius walk's per-node resolution.
	for _, position := range catalog.byEndpoint[identity] {
		call := catalog.records[position]
		if (call.edge.SourceFile == "" && call.edge.Synth == "") || call.from.Name != caller || call.from.ID == identity {
			continue
		}
		if call.to.ID != identity && !exportTouchesRoot(call.to, map[string]bool{identity: true}) {
			continue
		}
		if call.to.ID != identity || call.from.Resolution != "resolved" {
			opaque = true
			continue
		}
		if !seen[call.from.ID] {
			seen[call.from.ID] = true
			declarations = append(declarations, catalog.byID[call.from.ID])
		}
	}
	return declarations, opaque
}
