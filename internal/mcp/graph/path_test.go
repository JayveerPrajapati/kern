package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestPathUnknownSymbolErrorKeepsInput pins the unknown-symbol error shape:
// intel.Resolve returns "" for a symbol it cannot resolve, so the error must
// print the USER's input, never the post-Resolve (empty) value. Regression
// for the live defect: `meta "how do dispatch and nosuchsym connect end to
// end"` surfaced `kern: meta: unknown symbol:` with an empty name.
func TestPathUnknownSymbolErrorKeepsInput(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		from, to, wantIn string
	}{
		{"handleHealth", "nosuchsym", "unknown symbol: nosuchsym"},
		{"nosuchsym", "handleHealth", "unknown symbol: nosuchsym"},
		{"nosuchsym", "alsonothere", "unknown symbol: nosuchsym"},
	}
	for _, tc := range cases {
		_, err := Path(context.Background(), ix, map[string]any{"from": tc.from, "to": tc.to})
		if err == nil {
			t.Fatalf("Path(%q -> %q) with an unknown symbol must error", tc.from, tc.to)
		}
		if !strings.Contains(err.Error(), tc.wantIn) {
			t.Errorf("Path(%q -> %q) error = %q, want it to contain %q", tc.from, tc.to, err.Error(), tc.wantIn)
		}
		if strings.Contains(err.Error(), "unknown symbol: ") && strings.HasSuffix(err.Error(), "unknown symbol: ") {
			t.Errorf("Path(%q -> %q) error prints an empty symbol name: %q", tc.from, tc.to, err.Error())
		}
	}
}

// TestPathResolvableSymbolsNoError pins the healthy side: both symbols
// resolving must never hit the unknown-symbol path (they may be unconnected,
// which is a rendered "no path found" answer, not an error).
func TestPathResolvableSymbolsNoError(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Path(context.Background(), ix, map[string]any{"from": "handleHealth", "to": "handleUsers"})
	if err != nil {
		t.Fatalf("Path with resolvable symbols must not error, got: %v", err)
	}
	if strings.Contains(out, "unknown symbol") {
		t.Fatalf("resolvable symbols must not hit the unknown-symbol path, got: %q", out)
	}
}
