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
	// Gaps is the FULL list of uncovered symbols (HotGaps plus the
	// zero-caller ones), sorted identically to HotGaps: callers desc, then
	// symbol asc. HotGaps is always a prefix of Gaps.
	Gaps []Gap `json:"gaps"`
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
// call edges — everything the tests exercise, transitively. Matching is on
// exact symbol names only.
func coveredSet(ix *index.Index) map[string]bool {
	covered := map[string]bool{}
	queue := []string{}
	mark := func(name string) {
		if !covered[name] {
			covered[name] = true
			queue = append(queue, name)
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
		for _, ce := range ix.Calls[cur] {
			mark(ce.Target)
		}
	}
	return covered
}

// isCovered reports whether a symbol is reached by tests, matching on its
// exact name.
func isCovered(covered map[string]bool, name string) bool {
	return covered[name]
}

// AnalyzeCoverage computes overall coverage and the untested hotspots.
func AnalyzeCoverage(ix *index.Index) *Coverage {
	covered := coveredSet(ix)
	// Hoisted once: building the file map per symbol below would be
	// O(len(Symbols)^2) on large repos.
	fileMap := buildFileMap(ix)
	var total, coveredN int
	var candidates, allGaps []Gap
	for _, s := range ix.Symbols {
		if isTestFile(s.File) || (s.Kind != "func" && s.Kind != "method") {
			continue
		}
		if s.Kind == "func" && s.Name == "main" {
			// Entry points are invoked by the runtime, never by callers
			// under test: ranking every func main as an "untested hotspot"
			// is noise.
			continue
		}
		name := s.FullName()
		total++
		if isCovered(covered, name) {
			coveredN++
			continue
		}
		n := len(prodCallersWithFileMap(ix, name, fileMap))
		gap := Gap{
			Symbol: name, Kind: s.Kind, File: s.File, Line: s.Line, Callers: n,
		}
		// Every uncovered symbol is part of the full gap list, even when
		// nothing calls it (n == 0) — only the hotspot ranking requires
		// production callers.
		allGaps = append(allGaps, gap)
		if n > 0 {
			candidates = append(candidates, gap)
		}
	}
	sortGaps := func(gaps []Gap) {
		sort.Slice(gaps, func(i, j int) bool {
			if gaps[i].Callers != gaps[j].Callers {
				return gaps[i].Callers > gaps[j].Callers
			}
			return gaps[i].Symbol < gaps[j].Symbol
		})
	}
	sortGaps(candidates)
	sortGaps(allGaps)
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
		Gaps:      allGaps,
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

// Render returns a compact human-readable coverage report, capped at 50
// listed entries.
func (c *Coverage) Render() string {
	return c.RenderLimited(50)
}

// RenderLimited renders the coverage report honoring an explicit cap: at
// most `limit` entries are shown in total (hotspots first, then the
// remaining uncovered symbols). The tail line reports how many uncovered
// symbols are not shown.
func (c *Coverage) RenderLimited(limit int) string {
	if limit <= 0 {
		limit = 10
	}
	var b strings.Builder
	fmt.Fprintf(&b, "coverage: %d/%d symbols reachable from tests (%.1f%%)\n",
		c.Covered, c.Total, c.Percent)
	if c.Uncovered == 0 {
		b.WriteString("no untested callable symbols\n")
		return b.String()
	}
	shown := 0
	hot := c.HotGaps
	if len(hot) > limit {
		hot = hot[:limit]
	}
	if len(hot) > 0 {
		b.WriteString("untested hotspots (called but never exercised):\n")
		for _, g := range hot {
			fmt.Fprintf(&b, "  %s %s %s:%d (%d callers)\n", g.Kind, g.Symbol, g.File, g.Line, g.Callers)
		}
		shown += len(hot)
	}
	// The full section names EVERY uncovered symbol, including the
	// zero-caller ones that never rank as hotspots. HotGaps is a prefix of
	// Gaps, so skip re-listing the entries already shown above.
	start := len(hot)
	if shown < limit && len(c.Gaps) > start {
		end := start + (limit - shown)
		if end > len(c.Gaps) {
			end = len(c.Gaps)
		}
		b.WriteString("untested symbols:\n")
		for _, g := range c.Gaps[start:end] {
			fmt.Fprintf(&b, "  %s %s %s:%d (%d callers)\n", g.Kind, g.Symbol, g.File, g.Line, g.Callers)
		}
		shown += end - start
	}
	if shown < c.Uncovered {
		fmt.Fprintf(&b, "  … and %d more untested symbols (use --json for the full list)\n", c.Uncovered-shown)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
