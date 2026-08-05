package intel

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TraceHot is one project symbol that appears in a runtime trace, overlaid
// with static impact: how much of the call graph its changes touch (blast
// radius), whether tests cover it, and its risk score.
type TraceHot struct {
	Symbol string  `json:"symbol"`
	Hits   int     `json:"hits"`
	Kind   string  `json:"kind"`
	File   string  `json:"file"`
	Line   int     `json:"line"`
	Blast  int     `json:"blast"`
	Risk   float64 `json:"risk"`
	Tested bool    `json:"tested"`
}

// TraceReport is the runtime-impact overlay: hot symbols from a trace mapped
// onto the static call graph.
type TraceReport struct {
	Root     string     `json:"root"`
	Source   string     `json:"source,omitempty"`
	Frames   int        `json:"frames"`
	Resolved int        `json:"resolved"`
	Hot      []TraceHot `json:"hot"`
}

var (
	qualifiedRe = regexp.MustCompile(`[A-Za-z0-9_/.*()\[\]]+\.[A-Za-z_][A-Za-z0-9_]*`)
	callSiteRe  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*\(`)
	tokenRe     = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// Trace overlays a runtime trace (pprof -top text, a crash stack trace, or a
// plain list of function names) onto the index. It extracts candidate symbol
// names, resolves them, and ranks them by frequency. This is kern's dependency
// free live-telemetry overlay: point it at a trace file and see which project
// symbols are hot, how far their blast radius reaches, and whether tests cover
// them.
func Trace(ix *index.Index, src, sourceName string, limit int) *TraceReport {
	frames := 0
	counts := map[string]int{}
	ordered := map[string]int{}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		frames++
		seen := map[string]bool{}
		for _, cand := range traceCandidates(trimmed) {
			if seen[cand] {
				continue
			}
			seen[cand] = true
			r, ok := Resolve(ix, cand)
			if !ok {
				continue
			}
			if _, exists := counts[r]; !exists {
				ordered[r] = len(ordered)
			}
			counts[r]++
		}
	}

	meta := map[string]index.Symbol{}
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
	}
	covered := coveredSet(ix)

	var hot []TraceHot
	for sym, hits := range counts {
		h := TraceHot{Symbol: sym, Hits: hits}
		if s, ok := meta[sym]; ok {
			h.Kind = s.Kind
			h.File = s.File
			h.Line = s.Line
		}
		if reach, _ := BlastRadius(ix, []string{sym}); len(reach) > 1 {
			h.Blast = len(reach) - 1
		}
		h.Risk = 1.0 + math.Log2(float64(len(prodCallers(ix, sym))+1))
		h.Tested = isCovered(covered, sym)
		hot = append(hot, h)
	}
	sort.Slice(hot, func(i, j int) bool {
		if hot[i].Hits != hot[j].Hits {
			return hot[i].Hits > hot[j].Hits
		}
		if hot[i].Symbol != hot[j].Symbol {
			return hot[i].Symbol < hot[j].Symbol
		}
		return ordered[hot[i].Symbol] < ordered[hot[j].Symbol]
	})
	if limit > 0 && len(hot) > limit {
		hot = hot[:limit]
	}

	return &TraceReport{
		Root:     ix.Root,
		Source:   sourceName,
		Frames:   frames,
		Resolved: len(hot),
		Hot:      hot,
	}
}

// traceCandidates extracts candidate symbol names from one trace line: the
// trailing identifier of a qualified name, bare identifiers followed by '('
// (call sites), and lines that are a single identifier.
func traceCandidates(line string) []string {
	var out []string
	for _, m := range qualifiedRe.FindAllString(line, -1) {
		if i := strings.LastIndexByte(m, '.'); i >= 0 {
			out = append(out, m[i+1:])
		}
	}
	for _, m := range callSiteRe.FindAllString(line, -1) {
		out = append(out, strings.TrimSuffix(m, "("))
	}
	if !strings.ContainsAny(line, " .") && tokenRe.MatchString(line) {
		out = append(out, tokenRe.FindString(line))
	}
	return out
}

// RenderTrace returns the runtime-impact overlay as aligned columns.
func RenderTrace(r *TraceReport) string {
	var b strings.Builder
	head := "trace overlay"
	if r.Source != "" {
		head += " (" + r.Source + ")"
	}
	fmt.Fprintf(&b, "%s: %d frames, %d project symbols hot\n\n", head, r.Frames, r.Resolved)
	if len(r.Hot) == 0 {
		b.WriteString("  (no frames resolved to project symbols — check the trace format)\n")
		return b.String()
	}
	for _, h := range r.Hot {
		loc := ""
		if h.File != "" {
			loc = fmt.Sprintf("%s:%d", h.File, h.Line)
		}
		tested := "no"
		if h.Tested {
			tested = "yes"
		}
		fmt.Fprintf(&b, "  %3d×  %-40s %-22s blast %-4d tested %-3s risk %.1f\n",
			h.Hits, h.Symbol, loc, h.Blast, tested, h.Risk)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
