package intel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ExploreReport is the single-call result for a symbol: verbatim source,
// call flow (callers + callees), and blast radius (transitive callers).
type ExploreReport struct {
	Symbol       string            `json:"symbol"`
	Resolved     string            `json:"resolved,omitempty"`
	Definition   index.Symbol      `json:"definition"`
	Source       string            `json:"source"`
	Callers      []string          `json:"callers"`
	Callees      []string          `json:"callees"`
	BlastRadius  []string          `json:"blast_radius"`
	BlastFiles   []string          `json:"blast_files"`
	NearestDepth map[string]int    `json:"nearest_depth,omitempty"`
	Stats        *index.TokenStats `json:"stats,omitempty"`
}

// Explore returns verbatim source, the direct call flow (callers and callees),
// and the transitive blast radius with affected files for a symbol. depth=0
// means unlimited.
func Explore(ix *index.Index, symbol string, depth, maxNodes int) (*ExploreReport, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	resolved, ok := Resolve(ix, symbol)
	if !ok {
		return nil, fmt.Errorf("unknown symbol: %s", symbol)
	}
	d, ok := findDef(ix, resolved)
	if !ok {
		return nil, fmt.Errorf("no definition found for: %s", resolved)
	}

	rep := &ExploreReport{
		Symbol:     symbol,
		Resolved:   resolved,
		Definition: d,
		Source:     ix.Context(resolved, 0),
		Callers:    cleanNames(ix.CallersFor(d)),
		Callees:    cleanNames(ix.CallsFor(d)),
	}

	radius, dist := BlastRadius(ix, []string{resolved})
	rep.NearestDepth = dist

	if depth > 0 {
		var capped []string
		for _, s := range radius {
			if dist[s] <= depth {
				capped = append(capped, s)
			}
		}
		rep.BlastRadius = capped
	} else {
		rep.BlastRadius = radius
	}
	if maxNodes > 0 && len(rep.BlastRadius) > maxNodes {
		rep.BlastRadius = rep.BlastRadius[:maxNodes]
	}
	rep.BlastFiles = AffectedFiles(ix, rep.BlastRadius)
	return rep, nil
}

// findDef returns the first symbol matching a resolved FullName.
func findDef(ix *index.Index, full string) (index.Symbol, bool) {
	for _, s := range ix.Symbols {
		if s.FullName() == full {
			return s, true
		}
	}
	return index.Symbol{}, false
}

// cleanNames strips package qualifiers to simple names for display.
func cleanNames(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, n := range in {
		simple := simpleName(n)
		if !seen[simple] {
			seen[simple] = true
			out = append(out, simple)
		}
	}
	sort.Strings(out)
	return out
}

// RenderExplore renders the report as compact text.
func RenderExplore(r *ExploreReport) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "symbol: %s (%s %s:%d)\n\n",
		r.Resolved, r.Definition.Kind, r.Definition.File, r.Definition.Line)
	b.WriteString("== callers (" + strconv.Itoa(len(r.Callers)) + ") ==\n")
	b.WriteString(joinLines(r.Callers))
	b.WriteString("== callees (" + strconv.Itoa(len(r.Callees)) + ") ==\n")
	b.WriteString(joinLines(r.Callees))
	b.WriteString("== blast radius (" + strconv.Itoa(len(r.BlastRadius)) + " symbols, " + strconv.Itoa(len(r.BlastFiles)) + " files) ==\n")
	b.WriteString(joinLines(r.BlastRadius))
	if len(r.BlastFiles) > 0 {
		b.WriteString("== affected files ==\n")
		b.WriteString(joinLines(r.BlastFiles))
	}
	b.WriteString("== source ==\n")
	b.WriteString(r.Source)
	if !strings.HasSuffix(r.Source, "\n") {
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func joinLines(in []string) string {
	if len(in) == 0 {
		return "(none)\n"
	}
	return strings.Join(in, "\n") + "\n"
}
