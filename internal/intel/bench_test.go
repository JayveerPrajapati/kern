package intel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// benchFixture writes a hub-centric deterministic tree: a lib package whose
// Public symbol is called from every app file, plus local helpers that chain
// back through their callers. It mirrors the srcLib/srcClient shape used
// across the intel tests, scaled up so the query has a real graph to walk.
func benchFixture(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	var lib strings.Builder
	lib.WriteString("package lib\n\n")
	lib.WriteString("func Public() string { return inner() }\n\n")
	lib.WriteString("func inner() string { return \"x\" }\n\n")
	lib.WriteString("func Deep() { Public() }\n\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&lib, "func LibHelper%d() { Public() }\n\n", i)
	}
	write("lib/lib.go", lib.String())
	for c := 0; c < 12; c++ {
		var app strings.Builder
		fmt.Fprintf(&app, "package app\n\nimport \"lib\"\n\n")
		fmt.Fprintf(&app, "func Caller%d() {\n\tlib.Public()\n\tlib.LibHelper%d()\n}\n\n", c, c%8)
		fmt.Fprintf(&app, "func Local%d() { Caller%d() }\n", c, c)
		write(fmt.Sprintf("app/caller%d.go", c), app.String())
	}
	return dir
}

// buildBenchIndex builds the fixture once so the timed loop measures only the
// query, not graph construction.
func buildBenchIndex(b *testing.B) *index.Index {
	b.Helper()
	ix, err := index.Build(benchFixture(b))
	if err != nil {
		b.Fatal(err)
	}
	return ix
}

// BenchmarkGraphQuery measures the "what depends on X" query (BlastRadius,
// transitive callers) against the fixture's hub symbol.
func BenchmarkGraphQuery(b *testing.B) {
	ix := buildBenchIndex(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reach, _ := BlastRadius(ix, []string{"Public"})
		if len(reach) < 2 {
			b.Fatal("expected transitive callers for Public")
		}
	}
}

// BenchmarkGraphHubs measures hub ranking (caller count + score sort) on the
// same graph.
func BenchmarkGraphHubs(b *testing.B) {
	ix := buildBenchIndex(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if hubs := Hubs(ix, 10); len(hubs) == 0 {
			b.Fatal("expected hubs")
		}
	}
}

// benchFixtureScaled writes a deterministic 4-package tree sized to a
// realistic repo: hub (the shared library) plus three service packages, each
// 60 files of ~50 funcs — roughly 12k symbols. Every service file roots a
// local call chain at hub.Public(), so BlastRadius from the hub walks the
// whole graph, and the hub package carries a depth-10 caller chain
// (Chain0..Chain9) so long paths are exercised. Generation is deterministic
// (all names derive from loop indices) and fast: ~240 small files.
func benchFixtureScaled(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	const (
		pkgCount   = 4 // hub + 3 service packages
		filesPer   = 60
		symsPer    = 50
		chainDepth = 10
	)
	// hub/hub.go: the hub symbol, a depth-10 caller chain, and helpers that
	// call Public directly.
	{
		var hub strings.Builder
		hub.WriteString("package hub\n\n")
		hub.WriteString("func Public() string { return \"hub\" }\n\n")
		for c := 0; c < chainDepth; c++ {
			if c == 0 {
				fmt.Fprintf(&hub, "func Chain%d() { Public() }\n\n", c)
			} else {
				fmt.Fprintf(&hub, "func Chain%d() { Chain%d() }\n\n", c, c-1)
			}
		}
		for h := 0; h < 40; h++ {
			fmt.Fprintf(&hub, "func HubHelper%d() { Public() }\n\n", h)
		}
		write("hub/hub.go", hub.String())
	}
	// hub/hubf1..hubf59: ~50 funcs each, all reaching Public (even-indexed
	// call it directly, odd-indexed go through a helper), so the hub symbol
	// is called from every hub file too.
	for f := 1; f < filesPer; f++ {
		var hf strings.Builder
		fmt.Fprintf(&hf, "package hub\n\n")
		for i := 0; i < symsPer; i++ {
			if i%2 == 0 {
				fmt.Fprintf(&hf, "func HubF%d_%d() { Public() }\n\n", f, i)
			} else {
				fmt.Fprintf(&hf, "func HubF%d_%d() { HubHelper%d() }\n\n", f, i, i%40)
			}
		}
		write(fmt.Sprintf("hub/hubf%d.go", f), hf.String())
	}
	// svc1..svc3: 60 files each, ~50 funcs per file. F0 calls hub.Public(),
	// F1..F49 chain back to F0, and every 10th func calls cross-file into the
	// next file's same slot, so each package forms a dense web whose blast
	// radius from the hub covers every symbol.
	for p := 1; p < pkgCount; p++ {
		for f := 0; f < filesPer; f++ {
			var svc strings.Builder
			fmt.Fprintf(&svc, "package svc%d\n\nimport \"hub\"\n\n", p)
			for i := 0; i < symsPer; i++ {
				switch {
				case i == 0:
					fmt.Fprintf(&svc, "func S%dF%d_%d() { hub.Public() }\n\n", p, f, i)
				case i%10 == 5:
					fmt.Fprintf(&svc, "func S%dF%d_%d() { S%dF%d_%d() }\n\n", p, f, i, p, (f+1)%filesPer, i-5)
				default:
					fmt.Fprintf(&svc, "func S%dF%d_%d() { S%dF%d_%d() }\n\n", p, f, i, p, f, i-1)
				}
			}
			write(fmt.Sprintf("svc%d/svc%d_file%d.go", p, p, f), svc.String())
		}
	}
	return dir
}

// buildBenchIndexScaled builds the scaled fixture once so the timed loop
// measures only the query, not graph construction.
func buildBenchIndexScaled(b *testing.B) *index.Index {
	b.Helper()
	ix, err := index.Build(benchFixtureScaled(b))
	if err != nil {
		b.Fatal(err)
	}
	return ix
}

// assertScaledScale pins the scaled fixture to its documented size range so a
// future fixture edit cannot silently change what the benchmarks measure.
func assertScaledScale(b *testing.B, ix *index.Index) {
	b.Helper()
	n := len(ix.Symbols)
	b.Logf("scaled fixture symbol count: %d", n)
	if n < 5000 || n > 15000 {
		b.Fatalf("scaled fixture out of target range: %d symbols (want 5k-15k)", n)
	}
}

// BenchmarkGraphQueryScaled measures the transitive "what depends on X" query
// (BlastRadius) against the scaled hub: the BFS walks ~12k symbols across a
// depth-10 chain plus a broad fan-out of direct callers.
func BenchmarkGraphQueryScaled(b *testing.B) {
	ix := buildBenchIndexScaled(b)
	assertScaledScale(b, ix)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reach, _ := BlastRadius(ix, []string{"Public"})
		if len(reach) < 2 {
			b.Fatal("expected transitive callers for Public")
		}
	}
}

// BenchmarkGraphHubsScaled measures hub ranking over the scaled index: it
// scans every symbol, computes per-symbol caller counts and sorts by weight.
func BenchmarkGraphHubsScaled(b *testing.B) {
	ix := buildBenchIndexScaled(b)
	assertScaledScale(b, ix)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if hubs := Hubs(ix, 10); len(hubs) == 0 {
			b.Fatal("expected hubs")
		}
	}
}

// BenchmarkDirectDependOnScaled measures the direct (one-hop) reverse-caller
// lookup for the hub symbol — the cheapest dependency query, used by intel's
// "who calls X" path. The file map is hoisted out of the timed loop (see
// prodCallersWithFileMap's quadratic warning).
func BenchmarkDirectDependOnScaled(b *testing.B) {
	ix := buildBenchIndexScaled(b)
	assertScaledScale(b, ix)
	fileMap := buildFileMap(ix)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		callers := prodCallersWithFileMap(ix, "Public", fileMap)
		if len(callers) == 0 {
			b.Fatal("expected direct callers for Public")
		}
	}
}
