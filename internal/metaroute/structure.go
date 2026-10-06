package metaroute

import (
	"regexp"
	"strings"
)

// routeTokenRe matches an HTTP-style route path in a lowercase request, such
// as /v1/loop or /api/users/{id}. It requires a leading boundary so relative
// paths (internal/mcp) and "and/or" never match.
var routeTokenRe = regexp.MustCompile(`(?:^|\s)(/[a-z0-9_\-.{}:]+(?:/[a-z0-9_\-.{}:]+)*)`)

// extractRoute returns the first route path in a lowercase request, or "".
func extractRoute(low string) string {
	m := routeTokenRe.FindStringSubmatch(low)
	if m == nil {
		return ""
	}
	return strings.TrimRight(m[1], ".,;:!?")
}

// ClassifyStructureTools routes codebase-structure questions (import cycles,
// layering violations, hotspots) before the workflow router can claim them
// with a generic "analyze"/"plan"/"change" keyword. It defers to the existing
// routing whenever the request is an impact/what-if question or names a
// concrete symbol, so "what breaks if I change the cycle detector" and
// "how does ImportCycles work" keep their symbol-level routes.
func ClassifyStructureTools(low, request string) (string, map[string]any, bool) {
	if strings.Contains(low, "what breaks") || strings.Contains(low, "what if") ||
		HasWord(low, "impact") || HasWord(low, "change") || HasWord(low, "rename") ||
		ExtractSymbol(request, low) != "" {
		return "", nil, false
	}
	switch {
	case HasWord(low, "cycle") || HasWord(low, "cycles") || HasWord(low, "circular") || strings.Contains(low, "import graph") ||
		// Bare "layering" questions (no "violation" word) are structure
		// questions, not architecture-verify runs: "is the layering clean"
		// wants the import-cycle view. "layering violations" keeps the
		// architecture-verify arm below (pinned by structure_test.go).
		(HasWord(low, "layering") && !strings.Contains(low, "violation")):
		return "kern_cycles", map[string]any{}, true
	case strings.Contains(low, "layering") || strings.Contains(low, "layer violation") ||
		strings.Contains(low, "boundary violation") || strings.Contains(low, "architecture violation") ||
		strings.Contains(low, "architectural violation"):
		return "kern_verify", map[string]any{"types": "architecture"}, true
	case HasWord(low, "hotspot") || HasWord(low, "hotspots") || HasWord(low, "bottleneck") || HasWord(low, "bottlenecks") ||
		// Perf vocabulary (whole-word only — "profiles"/"profilers" never
		// match "profile", "workflow" never matches "slow"): slow/slowest,
		// performance/perf, profiling/profile. The old "slow path"/"slow
		// code"/"slow function" substrings are covered by HasWord("slow").
		HasWord(low, "slow") || HasWord(low, "slowest") || HasWord(low, "performance") || HasWord(low, "perf") ||
		HasWord(low, "profiling") || HasWord(low, "profile") ||
		HasWord(low, "fragile") || HasWord(low, "fragility") || strings.Contains(low, "bug-prone") || strings.Contains(low, "bug prone"):
		return "kern_fragility_hotspots", map[string]any{}, true
	// Flow/trace questions that name a concrete HTTP route search for that
	// route instead of the whole sentence ("how does a request to /v1/loop
	// flow through the system down to storage" → search "/v1/loop").
	case (strings.Contains(low, "flow") || strings.Contains(low, "trace")) && extractRoute(low) != "":
		return "kern_search", map[string]any{"query": extractRoute(low)}, true
	}
	return "", nil, false
}
