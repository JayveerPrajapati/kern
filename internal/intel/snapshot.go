package intel

import "github.com/JayveerPrajapati/kern/internal/domain"

// GraphSnapshot is the serializable construction state of a built Graph:
// exactly what FromIndex sets. The lazy query caches rebuild on first
// query, so a snapshot round-trip is behavior-identical to a fresh build.
type GraphSnapshot struct {
	Graph           domain.Graph
	Entries         map[string]bool
	PrecisionByLang map[string]string
}

// NewGraphFromSnapshot reconstructs a read-only Graph from a persisted
// snapshot.
func NewGraphFromSnapshot(s GraphSnapshot) Graph {
	return Graph{Graph: s.Graph, entries: s.Entries, precisionByLang: s.PrecisionByLang}
}

// Snapshot captures the graph's construction state for persistence.
func (g *Graph) Snapshot() GraphSnapshot {
	return GraphSnapshot{Graph: g.Graph, Entries: g.entries, PrecisionByLang: g.precisionByLang}
}
