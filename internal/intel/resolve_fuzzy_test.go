package intel

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// fuzzyQualifiedIndex mirrors the "bpcli/mcp.NewServer" defect shape: a
// production NewServer in a .../bpcli/mcp/ file, a same-named production
// symbol in an unrelated package, a test helper sharing the query's words in
// a .../bpcli/cli/ _test.go file, and a Test-prefixed symbol in the matching
// package.
func fuzzyQualifiedIndex() *index.Index {
	return &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			sym("func", "NewServer", "internal/bpcli/mcp/server.go", 81),
			sym("func", "NewServer", "internal/mcp/server.go", 446),
			sym("func", "newTestMCPServer", "internal/bpcli/cli/g13_test.go", 264),
			sym("func", "g5NewServer", "internal/bpcli/mcp/g5_test.go", 521),
			sym("func", "TestNewServerHelper", "internal/bpcli/mcp/server.go", 600),
		},
		Pkgs: map[string]*index.Pkg{
			"internal/bpcli/mcp": {Name: "mcp", Path: "internal/bpcli/mcp", Files: []string{"internal/bpcli/mcp/server.go", "internal/bpcli/mcp/g5_test.go"}, Lang: "go"},
			"internal/mcp":       {Name: "mcp", Path: "internal/mcp", Files: []string{"internal/mcp/server.go"}, Lang: "go"},
			"internal/bpcli/cli": {Name: "cli", Path: "internal/bpcli/cli", Files: []string{"internal/bpcli/cli/g13_test.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
}

// TestResolveFuzzyQualifiedNoCrossQualifierMatch is the F2 regression: a
// qualified query must never cross-qualifier fuzzy-match. "bpcli/mcp.NewServer"
// used to resolve to newTestMCPServer — a test helper in .../bpcli/cli — on
// shared words alone; it must resolve to a NewServer defined under
// .../bpcli/mcp/, and a path qualifier with no such fit must fail honestly.
func TestResolveFuzzyQualifiedNoCrossQualifierMatch(t *testing.T) {
	ix := fuzzyQualifiedIndex()
	got, ok := ResolveFuzzy(ix, "bpcli/mcp.NewServer")
	if !ok {
		t.Fatal("ResolveFuzzy(bpcli/mcp.NewServer) must resolve to the NewServer in .../bpcli/mcp/")
	}
	if got != "NewServer" {
		t.Fatalf(`ResolveFuzzy("bpcli/mcp.NewServer") = %q; want "NewServer" (never the .../bpcli/cli test helper)`, got)
	}
	// A path qualifier with no fit anywhere must fail honestly instead of
	// cross-matching into another package.
	if got, ok := ResolveFuzzy(ix, "sdk.NewServer"); ok {
		t.Errorf(`ResolveFuzzy("sdk.NewServer") = %q; want no resolution (no NewServer lives under .../sdk/)`, got)
	}
}

// TestResolveFuzzyTestSymbolsOnlyFallback is the F2 test-priority regression:
// test symbols (a _test.go file or a Test*/Benchmark* name) may only win a
// fuzzy resolution when no production symbol matched the query.
func TestResolveFuzzyTestSymbolsOnlyFallback(t *testing.T) {
	ix := fuzzyQualifiedIndex()
	// A production symbol wins even when a test symbol scores equally well
	// on the shared words.
	got, ok := ResolveFuzzy(ix, "NewServer")
	if !ok {
		t.Fatal("ResolveFuzzy(NewServer) must resolve")
	}
	if got != "NewServer" {
		t.Fatalf("ResolveFuzzy(NewServer) = %q; want the production NewServer, not %q", got, "a test symbol")
	}
	// A query whose only strong match is a test symbol falls back to it.
	got, ok = ResolveFuzzy(ix, "g5NewServer")
	if !ok || got != "g5NewServer" {
		t.Fatalf("ResolveFuzzy(g5NewServer) = %q, %v; want g5NewServer (test-only fallback)", got, ok)
	}
}

// TestResolveFuzzyPrefersProductionOverFixture is the fixture-directory
// regression: a plain .go file under testdata/ has production-shaped symbols
// (no _test.go suffix, no Test* name), so the old isTestSymbol check let it
// win fuzzy resolution over a real production symbol (live case: "what
// breaks if I change dispatch" resolved to
// internal/tasklife/testdata/resolve_prio/a.dispatch). The fixture must be a
// last-resort fallback like any other IsNonProduction symbol.
func TestResolveFuzzyPrefersProductionOverFixture(t *testing.T) {
	ix := &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			// Exact query-order name match: the fixture OUTRANKS the
			// production symbol (even after the ranked-search -100
			// fixture penalty), so it is iterated FIRST — only the
			// IsNonProduction demotion hands the resolution to production.
			sym("func", "GreetFixtureHello", "internal/tasklife/testdata/resolve_prio/a/a.go", 10),
			sym("func", "HelloGreetFixture", "internal/greet/greet.go", 10),
		},
		Pkgs: map[string]*index.Pkg{
			"internal/tasklife/testdata/resolve_prio/a": {Name: "a", Path: "internal/tasklife/testdata/resolve_prio/a", Files: []string{"internal/tasklife/testdata/resolve_prio/a/a.go"}, Lang: "go"},
			"internal/greet": {Name: "greet", Path: "internal/greet", Files: []string{"internal/greet/greet.go"}, Lang: "go"},
		},
		UpdatedAt: time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC),
	}
	got, ok := ResolveFuzzy(ix, "greet fixture hello")
	if !ok {
		t.Fatal("ResolveFuzzy(greet fixture hello) must resolve")
	}
	if got != "HelloGreetFixture" {
		t.Fatalf(`ResolveFuzzy("greet fixture hello") = %q; want the production HelloGreetFixture, not the testdata fixture GreetFixtureHello`, got)
	}
	// A query only the fixture fully matches still falls back to it —
	// fixture-only names keep resolving (parity with the entry chain's
	// TestExploreKeepsFixtureSymbolWhenNoProductionExists contract).
	ix.Symbols = append(ix.Symbols, sym("func", "GreetFixtureHelloWorld", "internal/tasklife/testdata/resolve_prio/a/a.go", 20))
	got, ok = ResolveFuzzy(ix, "greet fixture hello world")
	if !ok || got != "GreetFixtureHelloWorld" {
		t.Fatalf(`ResolveFuzzy("greet fixture hello world") = %q, %v; want GreetFixtureHelloWorld (fixture-only fallback)`, got, ok)
	}
}
