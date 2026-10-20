package topology

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// The helpers below are minimal copies of shared fixture builders that live
// in internal/intel's test files (writeTree, buildIndex, buildTestProject,
// containsStr) and internal/intel/bench_test.go (the bench fixtures). The
// moved topology tests need them; per the extraction rules no shared
// testutil package is created, so each package keeps its own copy.

// writeTree writes files under a temp dir and returns its path.
func writeTree(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// buildIndex builds a real index over root.
func buildIndex(t *testing.T, root string) *index.Index {
	t.Helper()
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// buildTestProject writes a minimal single-file Go project.
func buildTestProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	src := "package main\n\n// Greet says hello.\nfunc Greet() { println(\"hi\") }\n\n// helper is unused.\nfunc helper() {}\n\nfunc main() { Greet() }\n"
	_ = os.WriteFile(filepath.Join(root, "app.go"), []byte(src), 0o644)
	return root
}

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}

// benchFixture writes a hub-centric deterministic tree: a lib package whose
// Public symbol is called from every app file, plus local helpers that chain
// back through their callers.
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

// buildBenchIndex builds the bench fixture once so the timed loop measures
// only the query, not graph construction.
func buildBenchIndex(b *testing.B) *index.Index {
	b.Helper()
	ix, err := index.Build(benchFixture(b))
	if err != nil {
		b.Fatal(err)
	}
	return ix
}

// benchFixtureScaled writes a deterministic 4-package tree sized to a
// realistic repo (see the intel copy for the full rationale).
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
