package index

import (
	"github.com/JayveerPrajapati/kern/internal/asttypes"
)

// The shared AST data types (symbols, packages, edges, confidence) live in
// internal/asttypes. These aliases keep every index-internal and external
// reference (index.Symbol, index.CallEdge, index.ConfidenceHigh, ...)
// compiling unchanged.
type (
	Confidence = asttypes.Confidence
	CallEdge   = asttypes.CallEdge
	ImportEdge = asttypes.ImportEdge
	ExportEdge = asttypes.ExportEdge
)

const (
	ConfidenceHigh   = asttypes.ConfidenceHigh
	ConfidenceMedium = asttypes.ConfidenceMedium
	ConfidenceLow    = asttypes.ConfidenceLow
)

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

// CallEdgeTargets projects a call-edge slice onto its target names. The
// implementation lives in asttypes; this wrapper keeps existing callers
// (and older index serializations) unchanged.
func CallEdgeTargets(edges []CallEdge) []string {
	return asttypes.CallEdgeTargets(edges)
}
