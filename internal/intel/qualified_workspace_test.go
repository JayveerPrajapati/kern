package intel

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// rustWorkspaceIndex mirrors the Cargo-workspace shape: two same-named
// callables in different crates, both under <crate>/src/, one a free function
// and one an impl method, plus a `mod resolve;` declaration in the crate root.
func rustWorkspaceIndex() *index.Index {
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

// TestExploreRustWorkspaceQualifier drives the public entry point end to end and
// asserts the rendered note never claims a package-qualified match it did not
// make.
func TestExploreRustWorkspaceQualifier(t *testing.T) {
	ix := rustWorkspaceIndex()
	rep, err := Explore(ix, "densic-query.resolve", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Definition.File != "crates/densic-query/src/resolve.rs" {
		t.Fatalf("evidence must come from the qualified definition, got %s", rep.Definition.File)
	}
	if out := RenderExplore(rep); !strings.Contains(out, "(package-qualified match)") {
		t.Errorf("ancestor-tier match must be labelled package-qualified:\n%s", out)
	}
	rep, err = Explore(ix, "resolve.rs.resolve", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if out := RenderExplore(rep); strings.Contains(out, "(package-qualified match)") {
		t.Errorf("a qualifier that selected nothing must not be labelled package-qualified:\n%s", out)
	}
}
