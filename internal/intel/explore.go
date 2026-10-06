package intel

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/code"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
	"github.com/JayveerPrajapati/kern/internal/tokstats"
)

// ExploreReport is the single-call result for a symbol: verbatim source,
// call flow (callers + callees), and blast radius (transitive callers).
type ExploreReport struct {
	Symbol       string               `json:"symbol"`
	Resolved     string               `json:"resolved,omitempty"`
	Alternatives []string             `json:"alternatives,omitempty"` // other definitions a bare name also matches ("FullName (file:line)")
	Definition   index.Symbol         `json:"definition"`
	Source       string               `json:"source"`
	Callers      []string             `json:"callers"`
	CallerLocs   map[string]string    `json:"caller_locs,omitempty"` // caller → "file:line" (unresolved callers omitted)
	Callees      []string             `json:"callees"`
	CallerConf   map[string]string    `json:"caller_conf,omitempty"`  // caller → EXTRACTED/INFERRED/AMBIGUOUS
	CalleeConf   map[string]string    `json:"callee_conf,omitempty"`  // callee → EXTRACTED/INFERRED/AMBIGUOUS
	CallerSynth  map[string]string    `json:"caller_synth,omitempty"` // caller → router:chi (SYNTHESIZED dispatch)
	CalleeSynth  map[string]string    `json:"callee_synth,omitempty"` // callee → router:chi
	CalleeSkels  []string             `json:"callee_skels,omitempty"` // folded callee bodies, budget-permitting
	BlastRadius  []string             `json:"blast_radius"`
	BlastFiles   []string             `json:"blast_files"`
	NearestDepth map[string]int       `json:"nearest_depth,omitempty"`
	StaleBanner  string               `json:"stale_banner,omitempty"`
	Stats        *tokstats.TokenStats `json:"stats,omitempty"`
	Evidence     string               `json:"evidence,omitempty"` // P2 anchor: file:line + certificate for the subject
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

// ResolveEntry resolves a user-supplied query to the definition explore
// reports, applying the full entry-resolution policy: exact Resolve, a
// ResolveFuzzy fallback (every-query-word rule), a production-over-
// test/fixture re-point for bare names, and a package-qualifier refinement.
// It is the SINGLE shared entry resolver for kern explore and kern impact so
// both surfaces answer the same ambiguous query the same way (P2 entry
// parity) instead of each carrying its own private copy of the policy.
// ok is false when the query resolves to nothing (or to a name with no index
// definition); on success resolved is the final FullName and d the final
// definition, both refined by the policy above.
func ResolveEntry(ix *index.Index, query string) (resolved string, d *index.Symbol, ok bool) {
	resolved, ok = Resolve(ix, query)
	if !ok {
		// Exact resolution failed: a unique strong fuzzy match (every query
		// word matched) auto-resolves; a partial match must NOT substitute
		// an unrelated symbol.
		if cand, ok2 := ResolveFuzzy(ix, query); ok2 {
			resolved, ok = cand, true
		}
	}
	if !ok {
		return "", nil, false
	}
	def, found := findDef(ix, resolved)
	if !found {
		return "", nil, false
	}
	d = &def
	// A bare name that first lands on a test or fixture symbol (a free
	// `dispatch` under testdata/ beats the real Server.dispatch method on an
	// exact-name match) is re-pointed at a production definition when one
	// exists; a name that only exists in tests keeps its test symbol.
	if IsNonProduction(*d) && !strings.ContainsAny(query, "./") {
		if prod, found := firstProductionNamed(ix, query); found {
			d, resolved = &prod, prod.FullName()
		}
	}

	if q, found := qualifiedPackageDef(ix, query, *d); found {
		d, resolved = &q, q.FullName()
	}
	return resolved, d, true
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
	resolved, d, ok := ResolveEntry(ix, symbol)
	if !ok {
		// ResolveEntry folded the whole chain (Resolve → ResolveFuzzy →
		// findDef → production re-point → package-qualifier refinement);
		// distinguish its two failure modes exactly as before: a query that
		// resolves to a name with no index definition vs one that resolves
		// to nothing at all. The re-resolve only runs on the error path.
		if full, ok2 := Resolve(ix, symbol); ok2 {
			return nil, fmt.Errorf("no definition found for: %s", full)
		}
		return nil, fmt.Errorf("unknown symbol: %s", symbol)
	}

	rep := &ExploreReport{
		Symbol:       symbol,
		Resolved:     resolved,
		Alternatives: AmbiguousAlternatives(ix, symbol, *d),
		Definition:   *d,
		// Source must come from the SAME candidate as Definition: Context
		// re-resolves a bare name and could slice a different symbol (e.g.
		// TS interface definition + Go method source for "dispatch").
		Source:   ix.ContextDef(*d, 0),
		Evidence: AnchorLine(ix, resolved),
	}
	passes := MinConfidenceFilter(minConf)
	seenCallers := map[string]bool{}
	for _, c := range ix.CallersFor(*d) {
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
		// Per-caller file:line so the agent can jump straight to the call
		// site instead of re-searching each caller. Mirrors the
		// callee resolution below; an unresolved caller (cross-package
		// qualified ref, nodesForIDs limitation) is rendered without a
		// location — never fabricated.
		if def, ok := findCallerDef(ix, c, *d); ok && def.File != "" {
			if rep.CallerLocs == nil {
				rep.CallerLocs = map[string]string{}
			}
			rep.CallerLocs[name] = fmt.Sprintf("%s:%d", def.File, def.Line)
		}
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
	for _, c := range ix.CallsFor(*d) {
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
		if cs, ok := findCalleeDef(ix, c, *d); ok {
			calleeSyms = append(calleeSyms, cs)
		}
	}
	sort.Strings(rep.Callees)

	rep.fitBudget(ix, maxTokens, calleeSyms)

	radius, radiusSyms, dist, _ := blastRadiusWalk(ix, []string{resolved}, false)
	rep.NearestDepth = dist

	if depth > 0 {
		var capped []string
		var cappedSyms []index.Symbol
		for i, s := range radius {
			if dist[s] <= depth {
				capped = append(capped, s)
				cappedSyms = append(cappedSyms, radiusSyms[i])
			}
		}
		rep.BlastRadius, radiusSyms = capped, cappedSyms
	} else {
		rep.BlastRadius = radius
	}
	if maxNodes > 0 && len(rep.BlastRadius) > maxNodes {
		rep.BlastRadius = rep.BlastRadius[:maxNodes]
		radiusSyms = radiusSyms[:maxNodes]
	}
	// BlastFiles come from the RESOLVED radius symbols, not the names: a
	// bare name like "Generate" is shared by several packages, and a
	// first-match name→file map would attribute it to the wrong file
	// (receipt.go instead of evidence/bundle.go).
	rep.BlastFiles = affectedFilesOf(radiusSyms)
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
		rep.Stats = &tokstats.TokenStats{
			FullContext:   n,
			CompactTokens: n,
			SavingsPct:    0,
			Source:        "explore",
			Baseline:      "verbatim source",
		}
		return
	}
	rawSource := rep.Source
	rep.Source = budget.FitCode(rawSource, maxTokens)
	compact := tokenize.Count(rep.Source)
	full := tokenize.Count(rawSource)
	rep.Stats = &tokstats.TokenStats{
		FullContext:   full,
		CompactTokens: compact,
		SavingsPct:    savingsPct(full, compact),
		Source:        "explore",
		Baseline:      "verbatim source",
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

// savingsPct is the explore-side delegate of the shared token-savings
// formula (finding L2: one implementation, in internal/tokstats) so the intel
// surface can never drift from the graph/context footers.
func savingsPct(full, compact int) int {
	return tokstats.SavingsPercent(full, compact)
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

// findCallerDef resolves a caller endpoint to the symbol that owns the call.
// An exact full-name match wins. An ambiguous bare name (the simple name is
// shared by several packages) never resolves to the explored target itself —
// a caller never calls the symbol being explored, so "AuthorizeContext"
// calling governance.AuthorizeContext must resolve to the MCP wrapper, not
// back to the core (which would render a bogus self-caller). Remaining
// ambiguity prefers the caller whose file imports the target's package (a
// cross-package caller must import the callee), then falls back to the first
// match (findDef parity, no regression).
func findCallerDef(ix *index.Index, caller string, target index.Symbol) (index.Symbol, bool) {
	var cand []index.Symbol
	for _, s := range ix.Symbols {
		if s.FullName() == caller {
			cand = append(cand, s)
		}
	}
	switch len(cand) {
	case 0:
		return index.Symbol{}, false
	case 1:
		return cand[0], true
	}
	// Ambiguous bare name: the caller is never the callee it calls.
	if target.File != "" {
		rest := cand[:0:0]
		for _, s := range cand {
			if s.File != target.File || s.Line != target.Line {
				rest = append(rest, s)
			}
		}
		switch len(rest) {
		case 1:
			return rest[0], true
		case 0:
			return cand[0], true
		}
		cand = rest
		// Prefer the candidate in the target's own package directory: a
		// same-package caller ("intel.Explore" calling ExploreBudgeted)
		// needs no import, so it would lose the import-based tie-break to a
		// cross-package same-named symbol that merely imports the package.
		dir := filepath.Dir(target.File)
		var local []index.Symbol
		for _, s := range cand {
			if filepath.Dir(s.File) == dir {
				local = append(local, s)
			}
		}
		if len(local) == 1 {
			return local[0], true
		}
		if len(local) > 1 {
			cand = local
		}
		// Prefer the candidate whose file imports the target's package (a
		// cross-package caller must import the callee).
		if ix.ImportsByFile != nil {
			suffix := "/" + dir
			var imp []index.Symbol
			for _, s := range cand {
				for _, e := range ix.ImportsByFile[s.File] {
					if strings.HasSuffix(e.Path, suffix) {
						imp = append(imp, s)
						break
					}
				}
			}
			if len(imp) == 1 {
				return imp[0], true
			}
		}
	}
	return cand[0], true // first-match fallback (findDef parity)
}

// findCalleeDef resolves a callee endpoint for skeleton fitting, preferring a
// same-package symbol when a bare name is shared across packages (a callee
// referenced by bare name from the target's source is its own package's
// symbol).
func findCalleeDef(ix *index.Index, callee string, target index.Symbol) (index.Symbol, bool) {
	var cand []index.Symbol
	for _, s := range ix.Symbols {
		if s.FullName() == callee {
			cand = append(cand, s)
		}
	}
	switch len(cand) {
	case 0:
		return index.Symbol{}, false
	case 1:
		return cand[0], true
	}
	if target.File != "" {
		dir := filepath.Dir(target.File)
		for _, s := range cand {
			if filepath.Dir(s.File) == dir {
				return s, true
			}
		}
	}
	return cand[0], true
}

// ResolveFuzzy resolves a bare name that is not in the index to the single
// strongest ranked-search candidate — but only when the candidate matched
// EVERY query word (MatchedAll) with a high score. A partial match is not
// evidence of identity: a query that shares two common words with an
// unrelated symbol ("NoSuchSymbolXYZ" matching "no"/"symbol" inside
// "TestSimulateRemoveSymbolNoBrokenCallSites") must fail resolution rather
// than silently substitute that symbol. Qualified queries ("pkg.Symbol",
// "dir/pkg.Symbol") must additionally not cross-qualifier match: the
// candidate's FullName has to end with the query's last dot-segment AND the
// query's prefix must suffix-match the candidate's file path by path
// segments, so "bpcli/mcp.NewServer" may only resolve to a NewServer defined
// in a .../bpcli/mcp/ file; no fit fails honestly. Test symbols AND testdata
// fixtures (_test.go files, Test*/Benchmark* names, testdata//fixtures/
// directories — the IsNonProduction predicate) are a last-resort fallback,
// after production symbols. Returns the candidate's FullName (falling back to
// its bare Name) and whether a fuzzy resolution happened.
func ResolveFuzzy(ix *index.Index, query string) (string, bool) {
	if ix == nil {
		return "", false
	}
	// L5: docs headings must not win fuzzy RESOLUTION over real code
	// symbols — a heading merely CONTAINS the query words (e.g. "kern_run"
	// matching a heading that names `kern check`), while a symbol IS the
	// name. Skip heading hits unless no real symbol matched at all.
	var headingFallback string
	// The same policy for non-production symbols: a test helper
	// (newTestMCPServer in a _test.go file) or a testdata/ fixture (a plain
	// .go file under testdata/ whose symbols look production-shaped) merely
	// SHARES the query's words; it may only win when no production symbol
	// matched at all.
	var testFallback string
	// A qualified query carries a scope the candidate must respect: last is
	// the query's final dot-segment ("NewServer" in "bpcli/mcp.NewServer")
	// and prefix is everything before it ("bpcli/mcp"). A '/'-only qualified
	// query has no dot-segment, and no symbol is named after a path, so it
	// fails honestly below.
	dot := strings.LastIndexByte(query, '.')
	qualified := strings.ContainsAny(query, "./")
	var last, prefix string
	if dot >= 0 {
		last, prefix = query[dot+1:], query[:dot]
	} else if qualified {
		last, prefix = "", query
	}
	for _, h := range RankedSearchScored(ix, query, 5) {
		if !(h.MatchedAll && h.Score >= 150) {
			continue
		}
		full := h.Symbol.FullName()
		if full == "" {
			full = h.Symbol.Name
		}
		if full == "" {
			continue
		}
		if qualified {
			if last == "" ||
				!strings.HasSuffix(strings.ToLower(full), strings.ToLower(last)) ||
				!pathSuffixMatch(h.Symbol.File, prefix) {
				continue
			}
		}
		if h.Symbol.Kind == "heading" {
			if headingFallback == "" {
				headingFallback = full
			}
			continue
		}
		// IsNonProduction (not just isTestSymbol): a plain .go file under
		// testdata/ has production-shaped symbols that isTestSymbol misses,
		// so a fixture must also be demoted to last-resort (live case:
		// "what breaks if I change dispatch" resolved to
		// tasklife/testdata/resolve_prio/a.dispatch via this exact path).
		if IsNonProduction(h.Symbol) {
			if testFallback == "" {
				testFallback = full
			}
			continue
		}
		return full, true
	}
	// A test symbol is still real code, so it outranks a docs heading.
	if testFallback != "" {
		return testFallback, true
	}
	if headingFallback != "" {
		return headingFallback, true
	}
	return "", false
}

// maxAlternatives caps how many other definitions an ambiguity note lists.
const maxAlternatives = 5

// ambiguousAlternatives lists the other production definitions a BARE query
// also matches, so the single definition explore picked is never presented as
// the only candidate. Qualified queries ("Type.Method", "pkg/dir.Symbol") name
// their target and return nil, as do test and heading symbols. The result is
// sorted and capped at maxAlternatives, with a trailing "+N more" entry.
func ambiguousAlternatives(ix *index.Index, query string, picked index.Symbol) []string {
	if ix == nil || query == "" || strings.ContainsAny(query, "./") {
		return nil
	}
	var alts []string
	for _, s := range ix.Symbols {
		if s.Name != query || s.Kind == "heading" || IsNonProduction(s) {
			continue
		}
		if s.File == picked.File && s.Line == picked.Line {
			continue
		}
		alts = append(alts, fmt.Sprintf("%s (%s:%d)", s.FullName(), s.File, s.Line))
	}
	sort.Strings(alts)
	if len(alts) > maxAlternatives {
		rest := len(alts) - maxAlternatives
		alts = append(alts[:maxAlternatives], fmt.Sprintf("+%d more", rest))
	}
	return alts
}

// AmbiguousAlternatives is the exported form of ambiguousAlternatives: the
// other production definitions a BARE query also matches, so a resolved
// definition is never presented as the only candidate. kern impact's render
// uses it to surface the same ambiguity note explore shows (P2 entry
// parity), keeping one shared source of the alternatives list.
func AmbiguousAlternatives(ix *index.Index, query string, picked index.Symbol) []string {
	return ambiguousAlternatives(ix, query, picked)
}

// isFixtureFile reports whether a path lives under a test-data directory
// (testdata/, fixtures/), whose symbols are sample inputs, not real code.
func isFixtureFile(file string) bool {
	lower := "/" + strings.ToLower(filepath.ToSlash(file))
	for _, d := range []string{"/testdata/", "/fixtures/", "/fixture/"} {
		if strings.Contains(lower, d) {
			return true
		}
	}
	return false
}

// IsNonProduction is true for test symbols and fixture-directory symbols
// (testdata/, fixtures/, fixture/) — the same predicate the entry resolution
// chain uses to re-point a bare name at a production definition. Exported so
// kern impact's node-ID mapping applies the identical production preference
// (the live store.New bug: impact used a testdata-only fixture predicate and
// let a /fixture/ symbol win where explore's policy demoted it).
func IsNonProduction(s index.Symbol) bool {
	return isTestSymbol(s) || isFixtureFile(s.File)
}

// firstProductionNamed returns the first production func/method/type whose
// bare Name equals name.
func firstProductionNamed(ix *index.Index, name string) (index.Symbol, bool) {
	for _, s := range ix.Symbols {
		if s.Name == name && s.Kind != "heading" && !IsNonProduction(s) {
			return s, true
		}
	}
	return index.Symbol{}, false
}

// isTestSymbol reports whether a symbol is test-only: defined in a test file
// (IsTestFile) or named with a Go test-function prefix (Test*/Benchmark*).
func isTestSymbol(s index.Symbol) bool {
	return IsTestFile(s.File) ||
		strings.HasPrefix(s.Name, "Test") ||
		strings.HasPrefix(s.Name, "Benchmark")
}

// qualifiedPackageDef honors a package qualifier ("verifycmd.checkRule",
// "internal/verifycmd.checkRule"): when the picked definition d lives in a
// different package, it returns the same-named free symbol whose directory
// ends with the qualifier. Type.Method queries (qualifier equals the receiver)
// and unqualified names never re-point.
func qualifiedPackageDef(ix *index.Index, symbol string, d index.Symbol) (index.Symbol, bool) {
	i := strings.LastIndexByte(symbol, '.')
	if i <= 0 || i+1 >= len(symbol) {
		return index.Symbol{}, false
	}
	qual, name := symbol[:i], symbol[i+1:]
	if d.Name != name || d.Receiver == qual || pathSuffixMatch(d.File, qual) {
		return index.Symbol{}, false
	}
	for _, s := range ix.Symbols {
		if s.Name == name && s.Receiver == "" && s.Kind != "heading" &&
			pathSuffixMatch(s.File, qual) && !IsNonProduction(s) {
			return s, true
		}
	}
	return index.Symbol{}, false
}

// pathSuffixMatch reports whether the directory part of file ends with the
// '/'-separated segments of prefix ("internal/bpcli/mcp/server.go" carries
// the prefix "bpcli/mcp"). An empty prefix matches every file.
func pathSuffixMatch(file, prefix string) bool {
	if prefix == "" {
		return true
	}
	segs := strings.Split(strings.Trim(prefix, "/"), "/")
	if len(segs) == 0 {
		return true
	}
	fsegs := strings.Split(strings.Trim(filepath.Dir(file), "/"), "/")
	if len(fsegs) < len(segs) {
		return false
	}
	off := len(fsegs) - len(segs)
	for i, s := range segs {
		if fsegs[off+i] != s {
			return false
		}
	}
	return true
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
	if r.Symbol != r.Resolved {
		// The user asked for one symbol and got another (fuzzy resolution):
		// surface the mapping before the report so the substitution is never
		// silent.
		how := "fuzzy match"
		switch {
		case r.Resolved == r.Symbol || strings.HasSuffix(r.Resolved, "."+r.Symbol):
			how = "exact name"
		case strings.ContainsAny(r.Symbol, "./") && strings.HasSuffix(r.Symbol, "."+simpleName(r.Resolved)):
			how = "package-qualified match"
		}
		fmt.Fprintf(&b, "resolved %s -> %s (%s)\n", r.Symbol, r.Resolved, how)
	}
	if len(r.Alternatives) > 0 {
		fmt.Fprintf(&b, "note: %q is ambiguous — showing %s; other definitions: %s. Pass a qualified name (Type.Method or dir/pkg.Symbol) to choose.\n",
			r.Symbol, r.Resolved, strings.Join(r.Alternatives, ", "))
	}
	fmt.Fprintf(&b, "symbol: %s (%s %s:%d)\n\n",
		r.Resolved, r.Definition.Kind, r.Definition.File, r.Definition.Line)
	// The callers count is UNIQUE SIMPLE NAMES of DIRECT callers: the loop
	// above dedupes CallersFor (the index's direct callers) by simpleName,
	// so "pkg1.Foo" + "pkg2.Foo" collapse to one "Foo". Labeling that
	// semantics stops this count from being mistaken for impact's
	// graph-node caller count, which counts a different universe (P2).
	b.WriteString("== callers (" + strconv.Itoa(len(r.Callers)) + ", unique simple names of direct callers) ==\n")
	b.WriteString(joinLinesConf(r.Callers, r.CallerConf, r.CallerSynth, r.CallerLocs))
	b.WriteString("== callees (" + strconv.Itoa(len(r.Callees)) + ") ==\n")
	b.WriteString(joinLinesConf(r.Callees, r.CalleeConf, r.CalleeSynth, nil))
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
// so every hop in the answer is FACT/INFERENCE-classifiable. locs carries
// each row's "file:line" ("" when the definition could not be resolved),
// rendered as " — file:line" after the label — nil for lists rendered
// name-only (callees stay unchanged).
func joinLinesConf(in []string, conf, synth, locs map[string]string) string {
	if len(in) == 0 {
		return "(none)\n"
	}
	var b strings.Builder
	for _, n := range in {
		b.WriteString(n)
		if label := conf[n]; label != "" {
			b.WriteString(" [" + label + "]")
		}
		if loc := locs[n]; loc != "" {
			b.WriteString(" — " + loc)
		}
		if s := synth[n]; s != "" {
			b.WriteString(" (SYNTHESIZED: " + s + ")")
		}
		b.WriteString("\n")
	}
	return b.String()
}
