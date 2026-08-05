package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Coverage is the test-coverage view of the call graph: which symbols are
// reachable from test code (directly or transitively).
type Coverage struct {
	Total     int     `json:"total"`
	Covered   int     `json:"covered"`
	Uncovered int     `json:"uncovered"`
	Percent   float64 `json:"percent"`
	HotGaps   []Gap   `json:"hot_gaps"`
}

// Gap is an uncovered symbol ranked by how many callers depend on it.
type Gap struct {
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Callers int    `json:"callers"`
}

// coveredSet returns the set of symbols reachable from any test function via
// call edges — i.e. everything the tests exercise, transitively. Callees are
// recorded under both their full name and their simple name, so a call like
// `ix.addFile(...)` matches the method symbol `Index.addFile`.
func coveredSet(ix *index.Index) map[string]bool {
	covered := map[string]bool{}
	queue := []string{}
	var mark func(name string)
	mark = func(name string) {
		if !covered[name] {
			covered[name] = true
			queue = append(queue, name)
		}
		if simple := simpleName(name); simple != name {
			mark(simple)
		}
	}
	for _, s := range ix.Symbols {
		if isTestFile(s.File) && (s.Kind == "func" || s.Kind == "method") {
			mark(s.FullName())
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, callee := range ix.Calls[cur] {
			mark(callee)
		}
	}
	return covered
}

// simpleName returns the part after the last '.' ("" for a plain name).
func simpleName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// isCovered reports whether a symbol is reached by tests, matching on the
// full name or its simple name.
func isCovered(covered map[string]bool, name string) bool {
	if covered[name] {
		return true
	}
	return covered[simpleName(name)]
}

// AnalyzeCoverage computes overall coverage and the untested hotspots.
func AnalyzeCoverage(ix *index.Index) *Coverage {
	covered := coveredSet(ix)
	var total, coveredN int
	var candidates []Gap
	for _, s := range ix.Symbols {
		if isTestFile(s.File) || (s.Kind != "func" && s.Kind != "method") {
			continue
		}
		name := s.FullName()
		total++
		if isCovered(covered, name) {
			coveredN++
			continue
		}
		if n := len(prodCallers(ix, name)); n > 0 {
			candidates = append(candidates, Gap{
				Symbol: name, Kind: s.Kind, File: s.File, Line: s.Line, Callers: n,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Callers != candidates[j].Callers {
			return candidates[i].Callers > candidates[j].Callers
		}
		return candidates[i].Symbol < candidates[j].Symbol
	})
	percent := 0.0
	if total > 0 {
		percent = float64(coveredN) / float64(total) * 100
	}
	return &Coverage{
		Total:     total,
		Covered:   coveredN,
		Uncovered: total - coveredN,
		Percent:   percent,
		HotGaps:   candidates,
	}
}

// TestGaps returns the top `limit` untested hotspots (ranked by callers).
func TestGaps(ix *index.Index, limit int) []Gap {
	if limit <= 0 {
		limit = 10
	}
	gaps := AnalyzeCoverage(ix).HotGaps
	if len(gaps) > limit {
		gaps = gaps[:limit]
	}
	return gaps
}

// Render returns a compact human-readable coverage report.
func (c *Coverage) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "coverage: %d/%d symbols reachable from tests (%.1f%%)\n",
		c.Covered, c.Total, c.Percent)
	if c.Uncovered == 0 {
		b.WriteString("no untested callable symbols\n")
		return b.String()
	}
	b.WriteString("untested hotspots (called but never exercised):\n")
	for _, g := range c.HotGaps {
		fmt.Fprintf(&b, "  %s %s %s:%d (%d callers)\n", g.Kind, g.Symbol, g.File, g.Line, g.Callers)
	}
	if len(c.HotGaps) < c.Uncovered {
		fmt.Fprintf(&b, "  … and %d more untested symbols\n", c.Uncovered-len(c.HotGaps))
	}
	return strings.TrimSuffix(b.String(), "\n")
}
