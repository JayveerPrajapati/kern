package twin

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/intel"
)

// EntitiesManyStrict is EntitiesMany for callers that hold bare names rather
// than node IDs (the impact overlay). A bare name defined in more than one
// package is ambiguous, so it yields no entities instead of pooling every
// same-named definition's connections (New, Generate, Run would otherwise
// pull in unrelated services and their endpoints). Qualified references and
// bare names with a single defining package resolve as in EntitiesMany.
func EntitiesManyStrict(g *intel.Graph, symbols []string) [][]EntityInfo {
	out := make([][]EntityInfo, len(symbols))
	if g == nil {
		return out
	}
	eg := buildEntityGraph(g)
	for i, sym := range symbols {
		if !strings.Contains(sym, ".") && eg.ambiguousAcrossPackages(sym) {
			continue
		}
		ents, err := eg.entitiesForSymbol(sym)
		if err != nil {
			continue
		}
		out[i] = ents
	}
	return out
}

func (eg *entityGraph) ambiguousAcrossPackages(bare string) bool {
	first := ""
	for _, id := range eg.candidates(bare) {
		pkg := eg.pkgOf[id]
		if first == "" {
			first = pkg
			continue
		}
		if pkg != first {
			return true
		}
	}
	return false
}
