// Entity overlay for impact/what-if (Feature 3): after the pure code-graph
// pass computes the affected symbols, this file surfaces the twin entity
// nodes (API / DB table / service / deployment) implicated by those symbols
// and attaches them to the impact reports. All twin usage lives here in the
// app layer — whatif stays pure (no twin import).
package app

import (
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/twin"
	"github.com/JayveerPrajapati/kern/internal/whatif"
)

// entityAgg is the aggregation unit for the entity overlay: one twin entity
// deduplicated by (Kind, Name, File) with the set of code symbols that
// implicate it.
type entityAgg struct {
	kind    string
	name    string
	file    string
	symbols map[string]bool
}

// collectEntityHits queries the twin entity graph for every given symbol and
// aggregates the implicated entities, deduplicated by (Kind, Name, File) with
// the implicating symbol lists merged. Symbols absent from the graph (or with
// no twin connections) are skipped. The result is deterministic: sorted by
// (Kind, Name, File) with per-entity symbol lists sorted. Returns nil when no
// entity is implicated (zero-cost no-entity case).
func collectEntityHits(g *intel.Graph, symbols []string) []entityAgg {
	if g == nil {
		return nil
	}
	files := entityFiles(g)
	byKey := map[string]*entityAgg{}
	for _, sym := range symbols {
		ents, err := twin.Entities(g, sym)
		if err != nil {
			continue // symbol not in the graph — no twin connections
		}
		for _, e := range ents {
			file := files[e.Kind+"\x00"+e.Name]
			key := e.Kind + "\x00" + e.Name + "\x00" + file
			agg := byKey[key]
			if agg == nil {
				agg = &entityAgg{kind: e.Kind, name: e.Name, file: file, symbols: map[string]bool{}}
				byKey[key] = agg
			}
			agg.symbols[sym] = true
		}
	}
	out := make([]entityAgg, 0, len(byKey))
	for _, agg := range byKey {
		out = append(out, *agg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].kind != out[j].kind {
			return out[i].kind < out[j].kind
		}
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].file < out[j].file
	})
	return out
}

// attachWhatIfEntities overlays the twin entities implicated by the given
// symbols onto the what-if report's Entities field.
func attachWhatIfEntities(g *intel.Graph, symbols []string) []whatif.EntityImpact {
	hits := collectEntityHits(g, symbols)
	if len(hits) == 0 {
		return nil
	}
	out := make([]whatif.EntityImpact, 0, len(hits))
	for _, h := range hits {
		out = append(out, whatif.EntityImpact{
			Kind:    h.kind,
			Name:    h.name,
			File:    h.file,
			Symbols: sortedUnique(h.symbols),
		})
	}
	return out
}

// attachImpactEntities overlays the twin entities implicated by the given
// symbols onto the impact report's Entities field (domain.ImpactReport).
func attachImpactEntities(g *intel.Graph, symbols []string) []domain.EntityImpact {
	hits := collectEntityHits(g, symbols)
	if len(hits) == 0 {
		return nil
	}
	out := make([]domain.EntityImpact, 0, len(hits))
	for _, h := range hits {
		out = append(out, domain.EntityImpact{
			Kind:    h.kind,
			Name:    h.name,
			File:    h.file,
			Symbols: sortedUnique(h.symbols),
		})
	}
	return out
}

// sortedUnique returns the map's keys as a sorted, deduplicated slice.
func sortedUnique(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// entityFiles builds a deterministic map of "kind\x00name" → source file for
// every entity node in the graph. The file comes from the entity's own
// metadata when it carries one (API registration file), falling back to its
// twin "defined_in" edge to a file node; entities without a known file map to
// "".
func entityFiles(g *intel.Graph) map[string]string {
	fileByID := map[string]string{}
	for _, n := range g.Nodes {
		switch {
		case n.API != nil && n.API.File != "":
			fileByID[n.ID] = n.API.File
		case n.Symbol != nil && n.Symbol.File != "":
			fileByID[n.ID] = n.Symbol.File
		}
	}
	for _, e := range g.Edges {
		if f, ok := fileEndpoint(e.To); ok {
			if _, known := fileByID[e.From]; !known {
				fileByID[e.From] = f
			}
		}
		if f, ok := fileEndpoint(e.From); ok {
			if _, known := fileByID[e.To]; !known {
				fileByID[e.To] = f
			}
		}
	}
	keyByID := map[string]string{}
	for _, n := range g.Nodes {
		label := n.Label
		if label == "" {
			label = n.ID
		}
		keyByID[n.ID] = n.Kind + "\x00" + label
	}
	out := map[string]string{}
	for id, file := range fileByID {
		if key, ok := keyByID[id]; ok {
			out[key] = file
		}
	}
	return out
}

// fileEndpoint reports whether ref is a "file:<path>" reference and returns
// the path.
func fileEndpoint(ref string) (string, bool) {
	if strings.HasPrefix(ref, "file:") {
		return strings.TrimPrefix(ref, "file:"), true
	}
	return "", false
}
