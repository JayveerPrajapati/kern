package intel

import (
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Confidence labels, matching the index's standard provenance taxonomy
// (EXTRACTED = deterministic AST fact, INFERRED = resolved through inference
// or cross-package lookup, AMBIGUOUS = unresolved/phantom reference).
const (
	confExtracted = "EXTRACTED"
	confInferred  = "INFERRED"
	confAmbiguous = "AMBIGUOUS"
)

// EdgeSynthLabel returns the synthesized-dispatch provenance of the call
// edge from → to ("router:net-http", "router:chi", "router:http-route"), or
// "" when the edge is AST-extracted. Mirrors EdgeConfidenceLabel's
// canonicalization and direction handling so every renderer can annotate
// SYNTHESIZED hops alongside the confidence tier (typed-claims principle:
// a synthesized edge is an inference, never presented as EXTRACTED).
func EdgeSynthLabel(ix *index.Index, from, to string) string {
	if ix == nil {
		return ""
	}
	from = canonicalSimple(ix, from)
	to = canonicalSimple(ix, to)
	for _, e := range ix.Calls[from] {
		if canonicalSimple(ix, e.Target) == to && e.Synth != "" {
			return e.Synth
		}
	}
	for _, e := range ix.Calls[to] {
		if canonicalSimple(ix, e.Target) == from && e.Synth != "" {
			return e.Synth
		}
	}
	return ""
}

// EdgeConfidenceLabel returns the standard provenance label for the call edge
// from → to, preferring the parser's per-edge confidence recorded on the
// Calls map and falling back to the directory heuristic. Both endpoints are
// canonicalized first, so a qualified form ("lib.Public") matches the
// recorded simple form and vice versa. It is the single provenance source for
// every agent-facing renderer (explore, graphctx, path, why, probe), so all
// answers classify each hop as
// FACT(EXTRACTED)/INFERENCE(INFERRED)/AMBIGUOUS consistently.
func EdgeConfidenceLabel(ix *index.Index, from, to string) string {
	if ix == nil {
		return confAmbiguous
	}
	from = canonicalSimple(ix, from)
	to = canonicalSimple(ix, to)
	// Exact recorded edge, both directions (path answers follow edges
	// either way). The Calls map is keyed by the owner's FullName; the
	// recorded Target may carry the source's qualified form ("lib.Public"),
	// so compare canonically on both sides.
	for _, e := range ix.Calls[from] {
		if canonicalSimple(ix, e.Target) == to {
			return labelOf(e.Confidence.String())
		}
	}
	for _, e := range ix.Calls[to] {
		if canonicalSimple(ix, e.Target) == from {
			return labelOf(e.Confidence.String())
		}
	}
	// Fallback: resolution heuristic on the from-side file.
	fromFile := ""
	if d, ok := findDef(ix, from); ok {
		fromFile = d.File
	} else if s, ok := ix.ResolveName(from); ok {
		fromFile = s.File
	}
	return labelOf(index.EdgeConfidenceHeuristic(ix, fromFile, to))
}

// canonicalSimple returns from's canonical FullName when it resolves, or from
// unchanged.
func canonicalSimple(ix *index.Index, name string) string {
	if r, ok := Resolve(ix, name); ok {
		return r
	}
	return name
}

// labelOf maps an internal tier (HIGH/MEDIUM/LOW, case-insensitive) to the
// standard label. The empty value renders AMBIGUOUS so an edge without
// recorded provenance is never mistaken for a high-confidence fact.
func labelOf(tier string) string {
	switch strings.ToUpper(tier) {
	case "HIGH":
		return confExtracted
	case "MEDIUM":
		return confInferred
	case "LOW", "":
		return confAmbiguous
	default:
		return confAmbiguous
	}
}

// confTier ranks the provenance tiers so a minimum-confidence filter can
// compare them: EXTRACTED > INFERRED > AMBIGUOUS. The internal tiers
// (HIGH/MEDIUM/LOW) rank identically; unrecognized labels rank as AMBIGUOUS.
func confTier(label string) int {
	switch label {
	case confExtracted, "HIGH":
		return 3
	case confInferred, "MEDIUM":
		return 2
	default:
		return 1
	}
}

// MinConfidenceFilter returns a predicate reporting whether an edge label
// passes the given minimum-confidence threshold. min accepts the standard
// labels ("EXTRACTED", "INFERRED", "AMBIGUOUS") or the internal tiers
// ("high", "medium", "low"), case-insensitive. An empty or unrecognized
// threshold accepts everything — the filter is opt-in, never a silent
// default.
func MinConfidenceFilter(min string) func(label string) bool {
	threshold := confTier(strings.ToUpper(strings.TrimSpace(min)))
	if threshold <= 1 {
		return func(string) bool { return true }
	}
	return func(label string) bool {
		return confTier(label) >= threshold
	}
}
