// Package asttypes holds the shared AST-level data types of kern's index:
// symbols, packages, call/import/export edges and the parser confidence
// levels attached to them. The internal/index package aliases these types so
// downstream consumers (internal/intel, internal/web, cmd/kern, ...) keep
// referencing index.Symbol & co. unchanged.
package asttypes

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

// CallEdge is one parsed call from an owner symbol to a target callee,
// carrying the parser's confidence in the edge.
type CallEdge struct {
	Target     string     `json:"target"`
	Confidence Confidence `json:"confidence"`
	SourceFile string     `json:"source_file,omitempty"`
	Dynamic    bool       `json:"dynamic,omitempty"`
	FromID     string     `json:"from_id,omitempty"`
	ToID       string     `json:"to_id,omitempty"`
	Binding    string     `json:"binding,omitempty"`
	// Synth marks edges synthesized from framework dispatch registrations
	// (router tables, callback wiring) rather than syntactic calls:
	// "router:net-http", "router:chi", "router:http-route". Synthesized
	// edges must stay distinguishable from AST-extracted ones (typed-claims
	// principle) — agents render them as SYNTHESIZED, never as EXTRACTED.
	Synth string `json:"synth,omitempty"`
}

// ImportEdge is one parsed import relationship, carrying the parser's
// confidence that the file really imports the package.
type ImportEdge struct {
	Path       string     `json:"path"`
	Confidence Confidence `json:"confidence"`
	Alias      string     `json:"alias,omitempty"`
	Symbol     string     `json:"symbol,omitempty"`
}

// ExportEdge is one module-level export that matters for cross-file binding.
// Kind "default" records the local symbol the module exports as its default;
// "named" records explicit local declarations/lists or reexports from Path; "star" is
// `export * from Path`.
type ExportEdge struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	Path   string `json:"path,omitempty"`
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
