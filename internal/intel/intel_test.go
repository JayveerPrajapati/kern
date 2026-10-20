package intel

import (
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

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

const srcLib = `package lib

func Public() string {
	return inner()
}

func inner() string {
	return "x"
}

func Deep() {
	Public()
}

func UntestedHot() {}
`

const srcClient = `package client

import "lib"

func Caller() {
	lib.Public()
	lib.UntestedHot()
}

func LocalOnly() {}
`

const srcTest = `package lib

import "testing"

func TestPublic(t *testing.T) {
	if Public() == "" {
		t.Fail()
	}
}
`

func TestBlastRadiusTransitive(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	reach, dist := BlastRadius(ix, []string{"Public"})
	if len(reach) < 2 {
		t.Fatalf("expected transitive callers for Public, got %v", reach)
	}
	if dist["Caller"] != 1 {
		t.Errorf("Caller should be distance 1 from Public, got %d", dist["Caller"])
	}
	if dist["Deep"] != 1 {
		t.Errorf("Deep should be distance 1 from Public, got %d", dist["Deep"])
	}
}

// TestAffectedFilesExcludesDocFiles pins: documentation files (markdown,
// HTML) stay in the index graph, but are excluded from code blast-radius file
// lists. The fixture reproduces the fuzzy over-match that dragged doc pages
// (export_graph.html, docs/adr/*.md) into every impacted-file set: the
// README heading "Public" lexically precedes the Go func and would shadow it
// in the symbol→file map without the doc filter.
func TestAffectedFilesExcludesDocFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"README.md":  "# Public\n\nSee the docs.\n",
		"lib/lib.go": srcLib,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The markdown heading is a real symbol in the index…
	heading := false
	for _, s := range ix.Symbols {
		if s.Name == "Public" && s.Kind == "heading" && s.File == "README.md" {
			heading = true
		}
	}
	if !heading {
		t.Fatal("expected markdown heading symbol Public in README.md")
	}
	// …but it must never surface in the blast-radius file set.
	files := AffectedFiles(ix, []string{"Public", "inner"})
	for _, f := range files {
		if langparse.IsDocFile(f) {
			t.Errorf("doc file %q leaked into affected files: %v", f, files)
		}
	}
	found := false
	for _, f := range files {
		if f == "lib/lib.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("code file lib/lib.go missing from affected files: %v", files)
	}
}

func TestAnalyzeChanges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	report := AnalyzeChanges(ix, []string{"lib/lib.go"})
	if len(report.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(report.Changes))
	}
	c := report.Changes[0]
	if c.Blast == 0 {
		t.Error("expected a nonzero blast radius (Caller/Deep/TestPublic -> Public -> inner)")
	}
	if len(c.Symbols) == 0 {
		t.Error("expected changed symbols to be detected")
	}
	// Public+inner are covered via TestPublic; Deep and UntestedHot are gaps.
	if len(c.Gaps) == 0 {
		t.Errorf("expected test gaps (Deep, UntestedHot), got %v", c.Symbols)
	}
}

func TestReviewFitsBudget(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := Review(ix, []string{"client/client.go", "lib/lib.go"}, 2000)
	if out == "" {
		t.Fatal("expected non-empty review")
	}
	if !strings.Contains(out, "kern review") {
		t.Errorf("expected review header, got:\n%s", out)
	}
}

func TestCoverageFindsGaps(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Public + inner are covered via TestPublic. UntestedHot is called by
	// Caller but never exercised -> an untested hotspot. LocalOnly is also
	// uncovered but nobody calls it -> counted, not a hotspot.
	cov := AnalyzeCoverage(ix)
	hot := map[string]bool{}
	for _, g := range cov.HotGaps {
		hot[g.Symbol] = true
	}
	if !hot["UntestedHot"] {
		t.Errorf("UntestedHot should be an untested hotspot, got %v", hot)
	}
	if hot["LocalOnly"] {
		t.Errorf("LocalOnly has no callers and should not be a hotspot")
	}
	if cov.Uncovered < 2 {
		t.Errorf("expected at least 2 uncovered symbols, got %d", cov.Uncovered)
	}
}

func TestFlowsFromEntryPoints(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	flows := Flows(ix, 10, 8)
	if len(flows) == 0 {
		t.Fatal("expected at least one flow")
	}
	if flows[0].Depth < 2 {
		t.Errorf("expected a deep flow (Caller -> Public -> inner), got depth %d (%+v)", flows[0].Depth, flows[0].Path)
	}
}

func TestFlowsCycleTerminates(t *testing.T) {
	// Mutual recursion forms a cycle: main -> A -> B -> A -> ...
	src := `package main

func main() {
	A()
}

func A() {
	B()
}

func B() {
	A()
}
`
	dir := writeTree(t, map[string]string{"main.go": src})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	flows := Flows(ix, 10, 100)
	if len(flows) == 0 {
		t.Fatal("expected at least one flow")
	}
	// The cycle must be broken: no symbol may repeat within a reported path.
	for _, f := range flows {
		seen := map[string]bool{}
		for _, n := range f.Path {
			if seen[n] {
				t.Errorf("cycle not broken in path %v", f.Path)
			}
			seen[n] = true
		}
	}
}

func TestIsTestFile(t *testing.T) {
	cases := map[string]bool{
		"foo_test.go":     true,
		"pkg/foo_test.go": true,
		"app.test.ts":     true,
		"app.spec.rb":     true,
		"test_main.py":    true,
		"tests/test_x.py": true,
		"__tests__/a.js":  true,
		"lib.go":          false,
		"foo.go":          false,
		"main.ts":         false,
	}
	for rel, want := range cases {
		if got := IsTestFile(rel); got != want {
			t.Errorf("IsTestFile(%q) = %v, want %v", rel, got, want)
		}
	}
}
