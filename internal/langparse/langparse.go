// Package langparse holds kern's language parsing machinery: the regex/heuristic
// foreign-language extractor (with its tree-sitter fast path), the Go go/ast
// call-analysis helpers, framework entry-point detection and the Go/Java
// compiler-binding passes. It is a leaf of the index dependency graph:
// internal/index imports langparse (never the other way around), and the
// shared AST data types live in internal/asttypes.
package langparse

import (
	"github.com/JayveerPrajapati/kern/internal/asttypes"
)

// Aliases for the shared AST data types so the parsing code below keeps its
// bare references (Symbol, CallEdge, ConfidenceHigh, ...) unchanged.
type (
	Symbol     = asttypes.Symbol
	CallEdge   = asttypes.CallEdge
	ImportEdge = asttypes.ImportEdge
	ExportEdge = asttypes.ExportEdge
	Pkg        = asttypes.Pkg
	Confidence = asttypes.Confidence
)

const (
	ConfidenceHigh   = asttypes.ConfidenceHigh
	ConfidenceMedium = asttypes.ConfidenceMedium
	ConfidenceLow    = asttypes.ConfidenceLow
)
