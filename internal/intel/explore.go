package intel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
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
	CallerConf   map[string]string `json:"caller_conf,omitempty"`  // caller → EXTRACTED/INFERRED/AMBIGUOUS
	CalleeConf   map[string]string `json:"callee_conf,omitempty"`  // callee → EXTRACTED/INFERRED/AMBIGUOUS
	CallerSynth  map[string]string `json:"caller_synth,omitempty"` // caller → router:chi (SYNTHESIZED dispatch)
	CalleeSynth  map[string]string `json:"callee_synth,omitempty"` // callee → router:chi
	CalleeSkels  []string          `json:"callee_skels,omitempty"` // folded callee bodies, budget-permitting
	BlastRadius  []string          `json:"blast_radius"`
	BlastFiles   []string          `json:"blast_files"`
	NearestDepth map[string]int    `json:"nearest_depth,omitempty"`
	StaleBanner  string            `json:"stale_banner,omitempty"`
	Stats        *index.TokenStats `json:"stats,omitempty"`
	Evidence     string            `json:"evidence,omitempty"` // P2 anchor: file:line + certificate for the subject
}

// Explore returns verbatim source, the direct call flow (callers and callees),
// and the transitive blast radius with affected files for a symbol. depth=0
// means unlimited.
func Explore(ix *index.Index, symbol string, depth, maxNodes int) (*ExploreReport, error) {
	return ExploreMin(ix, symbol, depth, maxNodes, "")
}

// DefaultExploreBounds maps unset explore bounds to the P2-8 promotion
// defaults: depth 2 hops, 30 radius nodes. Negative depth means unset;
// non-positive maxNodes means unset (0 is the flags zero value, so an
// explicit --max 0 also takes the default — pass a large N for uncapped,
// or --depth 0 for an uncapped radius which the negative check preserves).
func DefaultExploreBounds(depth, maxNodes int) (int, int) {
	if depth < 0 {
		depth = 2
	}
	if maxNodes <= 0 {
		maxNodes = 30
	}
	return depth, maxNodes
}

// ExploreMin is Explore with a minimum-confidence filter: callers and callees
// whose provenance label ranks below the threshold (see MinConfidenceFilter)
// are pruned from the answer, so agents stop chasing AMBIGUOUS phantom
// references. Blast radius is left untouched — it is a transitive reach set,
// not a set of direct claims. An empty threshold keeps every edge.
func ExploreMin(ix *index.Index, symbol string, depth, maxNodes int, minConf string) (*ExploreReport, error) {
	return ExploreBudgeted(ix, symbol, depth, maxNodes, minConf, 0)
}

// ExploreBudgeted is the full form: ExploreMin with an adaptive token budget
// (CG-P0-3). When maxTokens > 0 the verbatim source is fitted with
// budget.FitCode (folded bodies first), and the budget's remainder carries
// FoldContent skeletons of the direct callees — so the answer shows the
// function in full plus what it calls without exceeding the budget. The
// report carries a TokenStats savings panel. maxTokens <= 0 keeps verbatim
// source and no skeletons.
func ExploreBudgeted(ix *index.Index, symbol string, depth, maxNodes int, minConf string, maxTokens int) (*ExploreReport, error) {
	if maxTokens < 0 {
		maxTokens = 0
	}
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
		// Source must come from the SAME candidate as Definition: Context
		// re-resolves a bare name and could slice a different symbol (e.g.
		// TS interface definition + Go method source for "dispatch").
		Source:   ix.ContextDef(d, 0),
		Evidence: AnchorLine(ix, resolved),
	}
	passes := MinConfidenceFilter(minConf)
	seenCallers := map[string]bool{}
	for _, c := range ix.CallersFor(d) {
		label := EdgeConfidenceLabel(ix, c, resolved)
		if !passes(label) {
			continue
		}
		name := simpleName(c)
		if seenCallers[name] {
			continue
		}
		seenCallers[name] = true
		rep.Callers = append(rep.Callers, name)
		if rep.CallerConf == nil {
			rep.CallerConf = map[string]string{}
		}
		rep.CallerConf[name] = label
		if synth := EdgeSynthLabel(ix, c, resolved); synth != "" {
			if rep.CallerSynth == nil {
				rep.CallerSynth = map[string]string{}
			}
			rep.CallerSynth[name] = synth
		}
	}
	sort.Strings(rep.Callers)
	seenCallees := map[string]bool{}
	var calleeSyms []index.Symbol
	for _, c := range ix.CallsFor(d) {
		label := EdgeConfidenceLabel(ix, resolved, c)
		if !passes(label) {
			continue
		}
		name := simpleName(c)
		if seenCallees[name] {
			continue
		}
		seenCallees[name] = true
		rep.Callees = append(rep.Callees, name)
		if rep.CalleeConf == nil {
			rep.CalleeConf = map[string]string{}
		}
		rep.CalleeConf[name] = label
		if synth := EdgeSynthLabel(ix, resolved, c); synth != "" {
			if rep.CalleeSynth == nil {
				rep.CalleeSynth = map[string]string{}
			}
			rep.CalleeSynth[name] = synth
		}
		if cs, ok := findDef(ix, c); ok {
			calleeSyms = append(calleeSyms, cs)
		}
	}
	sort.Strings(rep.Callees)

	rep.fitBudget(ix, maxTokens, calleeSyms)

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
	rep.StaleBanner = ix.StalenessBanner(append(append([]string{}, rep.Definition.File), rep.BlastFiles...))
	return rep, nil
}

// fitBudget fits the report's verbatim source to a token budget and fills
// the remainder with FoldContent skeletons of the direct callees (CG-P0-3).
// A nil/zero budget leaves everything verbatim and records no stats.
func (rep *ExploreReport) fitBudget(ix *index.Index, maxTokens int, calleeSyms []index.Symbol) {
	if maxTokens <= 0 {
		// Unbudgeted path keeps verbatim source, but the savings panel is
		// always populated (P2-8): the answer states its own token cost
		// even when nothing is folded.
		n := tokenize.Count(rep.Source)
		rep.Stats = &index.TokenStats{
			FullContext:   n,
			CompactTokens: n,
			SavingsPct:    0,
			Source:        "explore",
		}
		return
	}
	rawSource := rep.Source
	rep.Source = budget.FitCode(rawSource, maxTokens)
	compact := tokenize.Count(rep.Source)
	rep.Stats = &index.TokenStats{
		FullContext:   tokenize.Count(rawSource),
		CompactTokens: compact,
		SavingsPct:    savingsPct(tokenize.Count(rawSource), compact),
		Source:        "explore",
	}
	// Callee skeletons: folded bodies (signatures + collapsed bodies) of the
	// direct callees, appended while the budget has room. This replaces the
	// follow-up kern_context/read calls for judging depth-2 relevance.
	remaining := maxTokens - compact
	for _, cs := range calleeSyms {
		body := ix.Context(cs.FullName(), 0)
		if body == "" {
			continue
		}
		folded := string(code.FoldContent([]byte(body)))
		head := "== callee " + cs.FullName() + " ==\n"
		skelTokens := tokenize.Count(head) + tokenize.Count(folded)
		if skelTokens > remaining {
			continue
		}
		rep.CalleeSkels = append(rep.CalleeSkels, head+strings.TrimSuffix(folded, "\n"))
		remaining -= skelTokens
	}
}

func savingsPct(full, compact int) int {
	if full <= 0 {
		return 0
	}
	return (full - compact) * 100 / full
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

// RenderExplore renders the report as compact text.
func RenderExplore(r *ExploreReport) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	if r.StaleBanner != "" {
		b.WriteString(r.StaleBanner + "\n\n")
	}
	fmt.Fprintf(&b, "symbol: %s (%s %s:%d)\n\n",
		r.Resolved, r.Definition.Kind, r.Definition.File, r.Definition.Line)
	b.WriteString("== callers (" + strconv.Itoa(len(r.Callers)) + ") ==\n")
	b.WriteString(joinLinesConf(r.Callers, r.CallerConf, r.CallerSynth))
	b.WriteString("== callees (" + strconv.Itoa(len(r.Callees)) + ") ==\n")
	b.WriteString(joinLinesConf(r.Callees, r.CalleeConf, r.CalleeSynth))
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
	for _, skel := range r.CalleeSkels {
		b.WriteString("\n")
		b.WriteString(skel)
		b.WriteString("\n")
	}
	if r.Stats != nil && r.Stats.Summary() != "" {
		b.WriteString("\n")
		b.WriteString(r.Stats.Summary())
		b.WriteString("\n")
	}
	if r.Evidence != "" {
		b.WriteString("\n")
		b.WriteString(r.Evidence)
		b.WriteString("\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// RenderExploreExplain renders the report plus the why-rationale section —
// what the symbol is, who depends on it and why (see FormatWhy). One call
// answers what touches this, how, and why it exists (P2-8 --explain).
func RenderExploreExplain(r *ExploreReport, info WhyInfo) string {
	return RenderExplore(r) + "\n\n== why ==\n" + FormatWhy(info)
}

func joinLines(in []string) string {
	if len(in) == 0 {
		return "(none)\n"
	}
	return strings.Join(in, "\n") + "\n"
}

// joinLinesConf renders a name list with each row's provenance label
// appended as "[EXTRACTED]/[INFERRED]/[AMBIGUOUS]" when a label is recorded,
// so every hop in the answer is FACT/INFERENCE-classifiable.
func joinLinesConf(in []string, conf, synth map[string]string) string {
	if len(in) == 0 {
		return "(none)\n"
	}
	var b strings.Builder
	for _, n := range in {
		b.WriteString(n)
		if label := conf[n]; label != "" {
			b.WriteString(" [" + label + "]")
		}
		if s := synth[n]; s != "" {
			b.WriteString(" (SYNTHESIZED: " + s + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}
