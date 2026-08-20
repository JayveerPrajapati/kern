package domain

import "time"

// Provenance records how a graph node or edge was extracted, so downstream
// consumers can weigh facts from different sources. AST extraction is
// deterministic and gets confidence 1.0; inferred or runtime-derived facts carry
// lower confidence.
type Provenance struct {
	Source      string    // "ast", "tree-sitter", "git", "runtime", "manual"
	ExtractedAt time.Time // when the fact was extracted
	Extractor   string    // tool/version that extracted this
	Confidence  float64   // 1.0 for AST, lower for inferred
}

// VersionMetadata supports incremental updates and staleness detection of a
// graph. GraphHash is a stable hash of the graph content (sorted node IDs plus
// sorted edge keys), so two equal graphs always hash identically.
type VersionMetadata struct {
	GraphHash   string    // hash of the full graph
	CommitHash  string    // VCS commit when built (empty when untracked)
	BuiltAt     time.Time // when the graph was built
	SymbolCount int       // number of symbol nodes
	EdgeCount   int       // number of edges
}
