package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// footprintIndex mirrors the C5 (QA 2026-10-15 finding 10) defect shape:
// the same production type name defined in a small package that sorts FIRST
// in index order and in a central package that sorts later — bare "Widget"
// used to resolve to the small package's definition purely by alphabetical
// index order, exactly like bare "Server" resolving to internal/bpcli/mcp
// (23 symbols) over the central internal/mcp Server.
func footprintIndex() *index.Index {
	return &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			// Small package internal/aa: 2 symbols.
			sym("type", "Widget", "internal/aa/widget.go", 10),
			sym("func", "helper", "internal/aa/widget.go", 20),
			// Central package internal/zz: 5 symbols, same-named Widget.
			sym("type", "Widget", "internal/zz/widget.go", 5),
			sym("func", "m1", "internal/zz/other.go", 1),
			sym("func", "m2", "internal/zz/other.go", 2),
			sym("func", "m3", "internal/zz/other.go", 3),
			sym("func", "m4", "internal/zz/other.go", 4),
		},
	}
}

// TestResolveEntryPrefersLargestFootprint is the C5 regression: a bare name
// defined in several production packages resolves to the definition in the
// most central package (symbol count), not the first index-order match.
func TestResolveEntryPrefersLargestFootprint(t *testing.T) {
	ix := footprintIndex()
	resolved, d, ok := ResolveEntry(ix, "Widget")
	if !ok {
		t.Fatal("ResolveEntry(Widget) must resolve")
	}
	if d.File != "internal/zz/widget.go" {
		t.Fatalf("bare Widget resolved to %s:%d; want the central package def internal/zz/widget.go", d.File, d.Line)
	}
	if resolved != "Widget" {
		t.Fatalf("resolved = %q, want Widget (bare type FullName)", resolved)
	}
}

// TestResolveEntryFootprintTieKeepsIndexOrder pins the tie rule: equal-size
// packages keep the pre-C5 first-index-order pick (strict > in the footprint
// loop), so existing behavior is unchanged unless another definition is
// strictly more central.
func TestResolveEntryFootprintTieKeepsIndexOrder(t *testing.T) {
	ix := &index.Index{
		Root: "/kern",
		Symbols: []index.Symbol{
			sym("type", "Widget", "internal/aa/widget.go", 10),
			sym("type", "Widget", "internal/zz/widget.go", 5),
			sym("func", "x1", "internal/aa/x.go", 1),
			sym("func", "x2", "internal/zz/x.go", 1),
		},
	}
	_, d, ok := ResolveEntry(ix, "Widget")
	if !ok || d.File != "internal/aa/widget.go" {
		t.Fatalf("tie must keep the first-index-order pick internal/aa/widget.go, got %v (ok=%v)", d.File, ok)
	}
}

// TestResolveEntryFootprintSkipsQualifiedQuery pins the scope: a qualified
// query names its package, and the footprint rule fires on bare names only.
func TestResolveEntryFootprintSkipsQualifiedQuery(t *testing.T) {
	ix := footprintIndex()
	_, d, ok := ResolveEntry(ix, "aa.Widget")
	if !ok {
		t.Fatal("ResolveEntry(aa.Widget) must resolve")
	}
	if d.File != "internal/aa/widget.go" {
		t.Fatalf("qualified aa.Widget resolved to %s; want internal/aa/widget.go (the qualifier wins over footprint)", d.File)
	}
}
