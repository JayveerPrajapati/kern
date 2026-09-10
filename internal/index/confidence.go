package index

// Confidence expresses how reliable a parsed symbol, call edge or import
// relationship is. The parser assigns a level based on how directly the fact
// is stated in source:
//
//   - HIGH: explicit, statically verifiable facts — function definitions,
//     type declarations, direct call edges, import statements.
//   - MEDIUM: facts recovered through type inference or name-heuristic
//     matching — interface implementations, method calls resolved through
//     receiver/parameter types, regex-extracted calls.
//   - LOW: inferred relationships — regex-detected entry points, virtual
//     dispatch edges.
type Confidence string

const (
	ConfidenceHigh   Confidence = "HIGH"
	ConfidenceMedium Confidence = "MEDIUM"
	ConfidenceLow    Confidence = "LOW"
)

// String returns the level name. The zero value renders as LOW so a record
// without an explicit confidence is never mistaken for high-confidence data.
func (c Confidence) String() string {
	if c == "" {
		return string(ConfidenceLow)
	}
	return string(c)
}

// parseConfidence converts a persisted confidence string back to a
// Confidence. Empty values (rows written before confidence tracking existed)
// default to MEDIUM: the edge is real but its provenance is unknown, so it
// must not claim HIGH.
func parseConfidence(s string) Confidence {
	switch Confidence(s) {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
		return Confidence(s)
	default:
		return ConfidenceMedium
	}
}

// CallEdge is one parsed call from an owner symbol to a target callee,
// carrying the parser's confidence in the edge.
type CallEdge struct {
	Target     string     `json:"target"`
	Confidence Confidence `json:"confidence"`
}

// ImportEdge is one parsed import relationship, carrying the parser's
// confidence that the file really imports the package.
type ImportEdge struct {
	Path       string     `json:"path"`
	Confidence Confidence `json:"confidence"`
}

// CallEdgeTargets projects a call-edge slice onto its target names, matching
// the plain-string call lists older code consumed.
func CallEdgeTargets(edges []CallEdge) []string {
	if len(edges) == 0 {
		return nil
	}
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		out = append(out, e.Target)
	}
	return out
}
