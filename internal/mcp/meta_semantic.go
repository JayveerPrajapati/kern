// Semantic escalation for the kern_meta classifier.
//
// classifyMetaRequest is a deterministic keyword router: when no keyword
// branch matches, it falls back to kern_search with the raw request. The
// fallback misroutes novel phrasing that is semantically close to a tool but
// lexically invisible to the keyword table (e.g. "show me the implementation
// plan" never hits the plan keywords). This file adds a dependency-free
// semantic fallback: token-overlap cosine over each tool's name +
// description, with a conservative threshold and a two-token minimum — a
// deterministic, offline upgrade of the fallback only (the keyword router is
// untouched, and LLM-based disambiguation is deliberately NOT used: it would
// be provider-dependent and non-deterministic, violating the "deterministic
// things stay deterministic" principle).
//
// Explanation questions ("how does X work") keep their code-intent semantics:
// their candidate set is restricted to the symbol/search family so a
// subsystem listing tool can never capture a code question (the F-2-era
// "plain code questions fall back to search" contract is preserved).
package mcp

import (
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"sync"
)

// viaSemanticArg marks a semantic route inside classifyMetaRequest's args;
// handleMeta strips it before dispatch and surfaces the route in the
// classified line.
const viaSemanticArg = "via_semantic"

// semanticStopwords are high-frequency tokens that carry no routing signal.
var semanticStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true,
	"that": true, "what": true, "how": true, "does": true, "can": true,
	"you": true, "me": true, "get": true, "show": true, "find": true,
	"tell": true, "about": true, "from": true, "into": true, "your": true,
	"will": true, "would": true, "should": true, "there": true, "here": true,
	"some": true, "any": true, "all": true, "are": true, "was": true,
	"were": true, "been": true, "have": true, "has": true, "had": true,
	"its": true, "it's": true, "not": true, "no": true, "yes": true,
	"out": true, "up": true, "down": true, "over": true, "under": true,
	"then": true, "than": true, "them": true, "they": true, "when": true,
	"where": true, "which": true, "why": true, "please": true, "now": true,
	"very": true, "just": true, "also": true, "via": true, "per": true,
	"our": true, "their": true, "his": true, "her": true, "use": true,
	"using": true, "used": true, "need": true, "want": true, "make": true,
	"made": true, "new": true, "old": true, "good": true, "bad": true,
}

// stemWord applies a minimal deterministic suffix trim (s/es/ing/ed) so
// "changes" and "change" match. Never reduces below 4 characters.
func stemWord(w string) string {
	for _, suffix := range []string{"ing", "ed", "es", "s"} {
		if len(w) > 4 && strings.HasSuffix(w, suffix) {
			return w[:len(w)-len(suffix)]
		}
	}
	return w
}

// metaTokens tokenizes text for the semantic router: lowercase, split on
// non-alphanumerics, drop stopwords and tokens shorter than 3 characters,
// stem-lite each token, dedupe, sort.
func metaTokens(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if len(f) < 3 || semanticStopwords[f] {
			continue
		}
		f = stemWord(f)
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// metaCosSim is the cosine of the token-overlap vectors (binary TF).
func metaCosSim(q, t []string) float64 {
	if len(q) == 0 || len(t) == 0 {
		return 0
	}
	tset := make(map[string]bool, len(t))
	for _, x := range t {
		tset[x] = true
	}
	overlap := 0
	for _, x := range q {
		if tset[x] {
			overlap++
		}
	}
	if overlap == 0 {
		return 0
	}
	return float64(overlap) / sqrtF(float64(len(q))*float64(len(t)))
}

// sqrtF is a dependency-free square root (math.Sqrt is stdlib; keep imports
// minimal by inlining a 3-iteration Newton step — precise enough for cosine
// thresholds).
func sqrtF(x float64) float64 {
	if x <= 0 {
		return 0
	}
	g := x
	for i := 0; i < 3; i++ {
		g = (g + x/g) / 2
	}
	return g
}

// semanticMetaRoute scores the request against every tool's name +
// description tokens and returns the best match when it clears the
// conservative bar: cosine >= semanticThreshold AND at least two distinct
// overlapping tokens (a single shared word like "index" must never route).
// Explanation questions ("how does X work", "what is Y") restrict the
// candidate set to the code-intent family so subsystem listing tools cannot
// capture code questions (F-2 semantics preserved). The returned args place
// the request under the tool's first query-ish string parameter.
func semanticMetaRoute(request string) (tool string, args map[string]any, score float64, ok bool) {
	const semanticThreshold = 0.16
	q := metaTokens(request)
	if len(q) < 2 {
		return "", nil, 0, false
	}
	low := strings.ToLower(request)
	explanation := strings.Contains(low, "how does") || strings.Contains(low, "how do") ||
		strings.Contains(low, "what is") || strings.Contains(low, "what are") ||
		strings.Contains(low, "what does") || strings.Contains(low, "explain")

	bestTool := ""
	bestScore := 0.0
	bestOverlap := 0
	for _, t := range catalog.All {
		if explanation && !codeIntentTool(t.Name) {
			continue
		}
		tt := metaToolTokens(t)
		if len(tt) < 3 {
			continue
		}
		sc := metaCosSim(q, tt)
		if sc <= bestScore {
			continue
		}
		ov := overlapCount(q, tt)
		if sc < semanticThreshold || ov < 2 {
			continue
		}
		bestTool, bestScore, bestOverlap = t.Name, sc, ov
	}
	if bestTool == "" {
		return "", nil, 0, false
	}
	_ = bestOverlap
	args = map[string]any{firstQueryParam(bestTool, request): request}
	return bestTool, args, bestScore, true
}

// codeIntentTool reports whether the tool belongs to the symbol/search
// family eligible for explanation-question escalation.
func codeIntentTool(name string) bool {
	switch name {
	case "kern_explore", "kern_search", "kern_context", "kern_probe",
		"kern_why", "kern_code_graph", "kern_path", "kern_inherits",
		"kern_near", "kern_arch", "kern_walk", "kern_callers", "kern_callees",
		"kern_usage", "kern_health":
		return true
	}
	return false
}

// firstQueryParam returns the tool's first query-ish string parameter
// (query/request/symbol/change/intent/prompt), falling back to "query".
func firstQueryParam(toolName, request string) string {
	_ = request
	if t, ok := catalog.ByName(toolName); ok {
		for _, key := range []string{"query", "request", "symbol", "change", "intent", "prompt"} {
			if prop, ok := t.InputSchema["properties"].(map[string]any); ok {
				if _, ok := prop[key].(map[string]any); ok {
					return key
				}
			}
		}
	}
	return "query"
}

// metaToolTokens returns the precomputed token slice for a tool's
// name+description. Tokenizing all 145 descriptions on every semantic
// request was pure per-query waste: the strings are static between catalog
// registrations, so each tool's tokens are computed once and reused.
var (
	metaTokMu    sync.Mutex
	metaTokCache map[string][]string
)

func metaToolTokens(t catalog.Tool) []string {
	metaTokMu.Lock()
	if metaTokCache == nil {
		metaTokCache = make(map[string][]string, len(catalog.All))
	}
	if tt, ok := metaTokCache[t.Name]; ok {
		metaTokMu.Unlock()
		return tt
	}
	tt := metaTokens(t.Name + " " + t.Description)
	metaTokCache[t.Name] = tt
	metaTokMu.Unlock()
	return tt
}

// overlapCount returns the number of query tokens present in the tool tokens.
func overlapCount(q, t []string) int {
	tset := make(map[string]bool, len(t))
	for _, x := range t {
		tset[x] = true
	}
	n := 0
	for _, x := range q {
		if tset[x] {
			n++
		}
	}
	return n
}
