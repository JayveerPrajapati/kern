package brief

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestFrameworkEntriesSortedAndCapped(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Entry: true, Framework: "fastapi", Route: "/x", Name: "b_handler", File: "a.py", Line: 1},
			{Entry: true, Framework: "fastapi", Route: "/a", Name: "a_handler", File: "a.py", Line: 2},
			{Entry: true, Framework: "spring", Route: "/", Name: "Z", File: "A.java", Line: 1},
			{Entry: false, Framework: "", Name: "x", File: "a.py", Line: 3},
		},
	}
	out := frameworkEntries(ix)
	if len(out) != 3 {
		t.Fatalf("expected 3 framework entries, got %d", len(out))
	}
	// fastapi (a_handler, b_handler) sorted before spring.
	if out[0].fw != "fastapi" || out[0].name != "a_handler" {
		t.Fatalf("expected fastapi/a_handler first, got %+v", out[0])
	}
	if out[1].fw != "fastapi" || out[1].name != "b_handler" {
		t.Fatalf("expected fastapi/b_handler second, got %+v", out[1])
	}
}

func TestFrameworkEntriesCappedAt20(t *testing.T) {
	ix := &index.Index{Symbols: make([]index.Symbol, 25)}
	for i := range ix.Symbols {
		ix.Symbols[i] = index.Symbol{Entry: true, Framework: "fw", Route: "/r", Name: "h", File: "f.go", Line: i + 1}
	}
	out := frameworkEntries(ix)
	if len(out) != 20 {
		t.Fatalf("expected capped at 20, got %d", len(out))
	}
}

func TestIndexSectionHubsAndEntries(t *testing.T) {
	// A symbol "shared" called by two others -> hub (>1 caller).
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "shared", File: "a.go", Line: 1},
			{Kind: "func", Name: "f1", File: "a.go", Line: 2},
			{Kind: "func", Name: "f2", File: "a.go", Line: 3},
			{Kind: "func", Name: "main", File: "a.go", Line: 4},
		},
		Calls:    map[string][]string{"f1": {"shared"}, "f2": {"shared"}, "main": {"shared"}},
		Callers:  map[string][]string{"shared": {"f1", "f2", "main"}},
		FileHashes: map[string]string{"a.go": "h"},
	}
	out := indexSection(ix)
	if !strings.Contains(out, "Most-called (hubs):") {
		t.Fatalf("expected hub section, got %q", out)
	}
	if !strings.Contains(out, "shared") || !strings.Contains(out, "callers") {
		t.Fatalf("expected hub shared with caller count, got %q", out)
	}
	if !strings.Contains(out, "Entry points:") {
		t.Fatalf("expected entry points, got %q", out)
	}
}

func TestArchitectureSectionNonEmpty(t *testing.T) {
	// Build a real multi-package project whose call graph yields communities.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "go.mod", "module demo\n\ngo 1.22\n")
	write(t, dir, "main.go", "package main\n\nimport \"demo/svc\"\n\nfunc main() { svc.Do() }\n")
	write(t, dir, "svc/svc.go", "package svc\n\nfunc Do() {}\n")

	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := architectureSection(ix)
	// Communities or coupling should appear for a 2-package call graph.
	if !strings.Contains(out, "## Architecture") {
		t.Fatalf("expected architecture section, got %q", out)
	}
}

func TestArchitectureSectionEmptyNoCalls(t *testing.T) {
	ix := &index.Index{} // no calls
	if got := architectureSection(ix); got != "" {
		t.Fatalf("expected empty section without calls, got %q", got)
	}
}

func TestStatsSectionRecordsExistingStats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	b := &strings.Builder{}
	statsSection(b)
	if !strings.Contains(b.String(), "kern savings") {
		t.Fatalf("expected kern savings section, got %q", b.String())
	}
}

func TestDedupe(t *testing.T) {
	if got := dedupe([]string{"a", "b", "a", "c", "b"}); len(got) != 3 {
		t.Fatalf("expected 3 unique, got %v", got)
	}
}
