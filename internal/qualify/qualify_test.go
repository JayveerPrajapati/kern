package qualify

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func sym(kind, name, file string, line int) index.Symbol {
	return index.Symbol{Kind: kind, Name: name, File: file, Line: line, Lang: "go"}
}

func noTests(index.Symbol) bool { return false }

// rustWorkspace mirrors the defect shape found in a Cargo workspace: two
// same-named callables in different crates, both under <crate>/src/, one a free
// function and one an impl METHOD (the real shape), plus a same-named method on
// an unrelated type and a `mod resolve;` declaration in the crate root. Every
// crate's sources live in src/, so only the crate directory (an ANCESTOR of
// src/) distinguishes them.
func rustWorkspace() *index.Index {
	return &index.Index{
		Root: "/densic",
		Symbols: []index.Symbol{
			sym("func", "resolve", "crates/densic-mcp/src/server.rs", 793),
			{Kind: "method", Name: "resolve", Receiver: "SymbolIndex", File: "crates/densic-query/src/resolve.rs", Line: 102, Lang: "rust"},
			{Kind: "method", Name: "resolve", Receiver: "Paths", File: "crates/densic-core/src/paths.rs", Line: 22, Lang: "rust"},
			sym("module", "resolve", "crates/densic-query/src/lib.rs", 15),
		},
		UpdatedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
	}
}

func TestPathContains(t *testing.T) {
	cases := []struct {
		file, prefix string
		want         bool
	}{
		{"crates/densic-query/src/resolve.rs", "densic-query", true},
		{"crates/densic-query/src/resolve.rs", "crates/densic-query", true},
		{"crates/densic-query/src/resolve.rs", "crates", true},
		{"crates/densic-query/src/resolve.rs", "src", true},
		{"crates/densic-query/src/resolve.rs", "densic-mcp", false},
		{"crates/densic-query/src/resolve.rs", "resolve.rs", false}, // the file itself is not a directory
		{"crates/densic-query/src/resolve.rs", "", false},           // empty prefix is not a wildcard here
		{"crates/densic-query/src/resolve.rs", "query", false},      // partial segment
	}
	for _, tc := range cases {
		if got := PathContains(tc.file, tc.prefix); got != tc.want {
			t.Errorf("PathContains(%q, %q) = %v; want %v", tc.file, tc.prefix, got, tc.want)
		}
	}
}

// TestDefinitionAncestorTier is the regression: "densic-query.resolve" used to
// keep the arbitrary first candidate (server.rs) because the crate dir is not a
// TRAILING directory of ".../densic-query/src/", and the renderer then labelled
// that non-match "package-qualified match".
func TestDefinitionAncestorTier(t *testing.T) {
	ix := rustWorkspace()
	picked := ix.Symbols[0] // crates/densic-mcp/src/server.rs
	got, ok := Definition(ix, "densic-query.resolve", picked, noTests)
	if !ok {
		t.Fatal("densic-query.resolve must re-point into crates/densic-query/")
	}
	if got.File != "crates/densic-query/src/resolve.rs" {
		t.Errorf("densic-query.resolve -> %s; want crates/densic-query/src/resolve.rs", got.File)
	}
	// The full source-path form still works through the trailing tier.
	got, ok = Definition(ix, "crates/densic-query/src.resolve", picked, noTests)
	if !ok || got.File != "crates/densic-query/src/resolve.rs" {
		t.Errorf("crates/densic-query/src.resolve -> %v (%v); want the query resolver", got.File, ok)
	}
	// A type qualifier keeps Type.Method behaviour (no path re-point).
	if _, ok := Definition(ix, "Paths.resolve", ix.Symbols[1], noTests); ok {
		t.Error("Paths.resolve is a Type.Method query; it must not re-point by path")
	}
}

// TestDefinitionRefusesAmbiguousQualifier: a qualifier that names more than one
// candidate must not pick one — the ambiguity must survive.
func TestDefinitionRefusesAmbiguousQualifier(t *testing.T) {
	ix := rustWorkspace()
	ix.Symbols = append(ix.Symbols, sym("func", "resolve", "apps/worker/src/main.rs", 10))
	picked := ix.Symbols[0]
	if got, ok := Definition(ix, "src.resolve", picked, noTests); ok {
		t.Fatalf("src.resolve matched %s; want a refusal (ambiguous across crates)", got.File)
	}
	if how := How(ix, "src.resolve", picked, noTests); how != "fuzzy match" {
		t.Errorf("How(src.resolve) = %q; want fuzzy match", how)
	}
}

// TestHowOnlyClaimsRealMatches: a qualifier that selected nothing
// ("resolve.rs.resolve") must be labelled fuzzy, not package-qualified.
func TestHowOnlyClaimsRealMatches(t *testing.T) {
	ix := rustWorkspace()
	server := ix.Symbols[0]
	if how := How(ix, "resolve.rs.resolve", server, noTests); how != "fuzzy match" {
		t.Errorf("How(resolve.rs.resolve) = %q; want fuzzy match", how)
	}
	if how := How(ix, "densic-mcp.resolve", server, noTests); how != "package-qualified match" {
		t.Errorf("How(densic-mcp.resolve) = %q; want package-qualified match", how)
	}
}
