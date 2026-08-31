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
