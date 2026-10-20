package brief

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/stats"
	"github.com/JayveerPrajapati/kern/internal/topology"
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
	out, _ := frameworkEntries(ix)
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
		// Distinct (name, route) pairs so F2's dedupe does not collapse them
		// before the cap is exercised.
		ix.Symbols[i] = index.Symbol{Entry: true, Framework: "fw", Route: "/r" + strconv.Itoa(i), Name: "h" + strconv.Itoa(i), File: "f.go", Line: i + 1}
	}
	out, total := frameworkEntries(ix)
	if len(out) != 20 {
		t.Fatalf("expected capped at 20, got %d", len(out))
	}
	// F3-1: the pre-cap total is disclosed alongside the capped list.
	if total != 25 {
		t.Fatalf("expected total 25, got %d", total)
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
		Calls:      map[string][]index.CallEdge{"f1": {index.CallEdge{Target: "shared", Confidence: index.ConfidenceHigh}}, "f2": {index.CallEdge{Target: "shared", Confidence: index.ConfidenceHigh}}, "main": {index.CallEdge{Target: "shared", Confidence: index.ConfidenceHigh}}},
		Callers:    map[string][]string{"shared": {"f1", "f2", "main"}},
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

// TestIndexSectionExcludesTestAndFixtureHubs pins Phase 5: the digest's
// "Most-called (hubs)" must show only production-defined symbols. External
// call targets (T.Fatalf — called from every _test.go file but defined
// nowhere in the repo) must not appear, and neither may symbols defined in
// _test.go files or testdata/ fixtures.
func TestIndexSectionExcludesTestAndFixtureHubs(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "shared", File: "a.go", Line: 1},
			{Kind: "func", Name: "f1", File: "a.go", Line: 2},
			{Kind: "func", Name: "f2", File: "a.go", Line: 3},
			{Kind: "func", Name: "TestShared", File: "a_test.go", Line: 1},
			{Kind: "func", Name: "FixtureShared", File: "testdata/fix.go", Line: 1},
		},
		Callers: map[string][]string{
			"shared":        {"f1", "f2"},
			"TestShared":    {"f1", "f2"},
			"FixtureShared": {"f1", "f2"},
			"T.Fatalf":      {"f1", "f2", "TestShared"},
		},
		FileHashes: map[string]string{"a.go": "h", "a_test.go": "h", "testdata/fix.go": "h"},
	}
	out := indexSection(ix)
	if !strings.Contains(out, "shared") {
		t.Fatalf("expected production hub shared, got %q", out)
	}
	for _, bad := range []string{"TestShared", "FixtureShared", "T.Fatalf"} {
		if strings.Contains(out, bad) {
			t.Errorf("hub section must exclude %s, got %q", bad, out)
		}
	}
}

// TestIndexSectionAllTestOmitsHubHeader: when every symbol is test/fixture,
// the digest must not render a "Most-called (hubs):" section at all.
func TestIndexSectionAllTestOmitsHubHeader(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "TestA", File: "a_test.go", Line: 1},
			{Kind: "func", Name: "TestB", File: "b_test.go", Line: 1},
		},
		Callers:    map[string][]string{"TestA": {"TestB", "TestB2"}},
		FileHashes: map[string]string{"a_test.go": "h", "b_test.go": "h"},
	}
	out := indexSection(ix)
	if strings.Contains(out, "Most-called (hubs):") {
		t.Errorf("all-test index must omit the hub section, got %q", out)
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

func TestArchitectureSectionGatedLargeGraph(t *testing.T) {
	// A graph above the gate must skip community detection (minutes on big
	// repos) and render the CHEAP compact fallback — package count + top
	// packages + the interactive-explorer pointer — instead of the old
	// "(skipped …)" marker (the digest's most useful newcomer section).
	ix := &index.Index{Calls: map[string][]index.CallEdge{"main": {index.CallEdge{Target: "helper", Confidence: index.ConfidenceHigh}}}}
	for i := 0; i < archGateSymbols+1; i++ {
		ix.Symbols = append(ix.Symbols, index.Symbol{
			Kind: "func", Name: "f", File: "x.go", Line: i + 1,
		})
	}
	out := architectureSection(ix)
	if strings.Contains(out, "(skipped") {
		t.Fatalf("gated digest must render the compact fallback, not the skipped marker: %q", out)
	}
	if !strings.Contains(out, "packages/subsystems") || !strings.Contains(out, "kern graph --html") {
		t.Fatalf("expected compact fallback with package count and graph pointer, got %q", out)
	}
	if !strings.Contains(out, "## Architecture") {
		t.Fatalf("compact fallback must keep the Architecture section, got %q", out)
	}
}

func TestStatsSectionRecordsExistingStats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out := statsSection(t.TempDir())
	if !strings.Contains(out, "kern savings") {
		t.Fatalf("expected kern savings section, got %q", out)
	}
}

// TestStatsSectionNoMachineWideNumbers pins QA 6.7(a): the stats ledger is
// machine-wide (entries carry no project root), so the digest must NOT
// surface other projects' ops/tokens/$ numbers — it shows a pointer instead.
// (The M9 label was not enough: the numbers themselves leaked cross-project
// activity into a project digest.)
func TestStatsSectionShowsRootedProjectStats(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rec, err := stats.NewRecorder()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := rec.Record(stats.Entry{
		Time:         time.Now(),
		Operation:    stats.OpOptimizePrompt,
		Root:         root,
		BeforeTokens: 100,
		AfterTokens:  40,
	}); err != nil {
		t.Fatal(err)
	}
	out := statsSection(root)
	for _, want := range []string{
		"kern savings (project, last 30 days)",
		"0 tool calls",
		"1 optimization events",
		"60 tokens saved",
		"machine-wide view: `kern stats`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("project stats missing %q: %q", want, out)
		}
	}
}

func TestStatsSectionNoMachineWideNumbers(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out := statsSection(t.TempDir())
	for _, leak := range []string{" ops", "tokens saved", "$", "cost saved", "machine-wide (all projects): "} {
		if strings.Contains(out, leak) {
			t.Fatalf("digest stats section must not carry machine-wide numbers (%q), got %q", leak, out)
		}
	}
	if !strings.Contains(out, "kern stats") {
		t.Fatalf("stats section must point at `kern stats`, got %q", out)
	}
}

// TestDigestHubsOrderedByCallerCount pins QA 5.6(b): the digest's hub list
// ranks by ACTUAL caller count desc and drops 0-caller symbols. Here main
// calls ten helpers (raw weight 10) while shared has three callers (raw 6) —
// the community-weighted ranking would put main first; the digest must show
// shared (3 callers) first and never list main.
func TestDigestHubsOrderedByCallerCount(t *testing.T) {
	symbols := []index.Symbol{
		{Kind: "func", Name: "shared", File: "a.go", Line: 1},
		{Kind: "func", Name: "f1", File: "a.go", Line: 2},
		{Kind: "func", Name: "f2", File: "a.go", Line: 3},
		{Kind: "func", Name: "f3", File: "a.go", Line: 4},
		{Kind: "func", Name: "main", File: "a.go", Line: 5},
	}
	calls := map[string][]index.CallEdge{
		"f1":   {{Target: "shared", Confidence: index.ConfidenceHigh}},
		"f2":   {{Target: "shared", Confidence: index.ConfidenceHigh}},
		"f3":   {{Target: "shared", Confidence: index.ConfidenceHigh}},
		"main": {},
	}
	for i := 0; i < 10; i++ {
		tgt := "t" + strconv.Itoa(i)
		symbols = append(symbols, index.Symbol{Kind: "func", Name: tgt, File: "a.go", Line: 100 + i})
		calls["main"] = append(calls["main"], index.CallEdge{Target: tgt, Confidence: index.ConfidenceHigh})
	}
	ix := &index.Index{
		Symbols:    symbols,
		Calls:      calls,
		Callers:    map[string][]string{"shared": {"f1", "f2", "f3"}, "main": {}},
		FileHashes: map[string]string{"a.go": "h"},
	}
	hubs := digestHubs(ix, 8)
	if len(hubs) == 0 {
		t.Fatal("expected hubs, got none")
	}
	for _, h := range hubs {
		if h.Callers == 0 {
			t.Errorf("0-caller symbol %s must not lead the digest hubs, got %+v", h.Symbol, hubs)
		}
	}
	if hubs[0].Symbol != "shared" || hubs[0].Callers != 3 {
		t.Fatalf("expected shared (3 callers) first, got %+v", hubs)
	}
	// The digest section itself renders the same ordering.
	out := indexSection(ix)
	if !strings.Contains(out, "shared (3 callers)") {
		t.Errorf("indexSection must show shared with 3 callers, got %q", out)
	}
	if strings.Contains(out, "main (0 callers)") {
		t.Errorf("indexSection must not list a 0-caller hub, got %q", out)
	}
}

// TestArchitectureSectionCommunitiesCapped pins QA 6.7(b): the architecture
// community list is bounded with a truncation pointer so a large repo cannot
// push the digest past its byte budget.
func TestArchitectureSectionCommunitiesCapped(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "main", File: "a.go", Line: 1},
			{Kind: "func", Name: "helper", File: "b.go", Line: 1},
		},
		Calls: map[string][]index.CallEdge{
			"main":   {{Target: "helper", Confidence: index.ConfidenceHigh}},
			"helper": {{Target: "main", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{"main": {"helper"}, "helper": {"main"}},
	}
	// Force more communities than the cap by patching the architecture
	// result: build via the real analysis then extend the slice.
	arch := topology.AnalyzeArchitecture(ix)
	for i := len(arch.Communities); i < 20; i++ {
		arch.Communities = append(arch.Communities, topology.ArchCommunity{ID: "c" + strconv.Itoa(i), Size: 1, Hub: "h", Packages: []string{"pkg"}})
	}
	if len(arch.Communities) <= 12 {
		t.Fatalf("fixture must exceed the community cap, got %d", len(arch.Communities))
	}
	out := renderArchitectureSection(&arch)
	if strings.Count(out, "size ") != 12 {
		t.Errorf("expected exactly 12 community rows, got %d:\n%s", strings.Count(out, "size "), out)
	}
	if !strings.Contains(out, "and 8 more communities") {
		t.Errorf("expected a truncation pointer, got %q", out)
	}
}

func TestDedupe(t *testing.T) {
	if got := dedupe([]string{"a", "b", "a", "c", "b"}); len(got) != 3 {
		t.Fatalf("expected 3 unique, got %v", got)
	}
}

func TestCallEdgesIsDirectedEdgeSum(t *testing.T) {
	ix := &index.Index{
		Calls: map[string][]index.CallEdge{
			"main":   {index.CallEdge{Target: "shared", Confidence: index.ConfidenceHigh}, index.CallEdge{Target: "helper", Confidence: index.ConfidenceHigh}},
			"f1":     {index.CallEdge{Target: "shared", Confidence: index.ConfidenceHigh}},
			"f2":     {index.CallEdge{Target: "shared", Confidence: index.ConfidenceHigh}},
			"shared": {index.CallEdge{Target: "deep", Confidence: index.ConfidenceHigh}},
		},
	}
	if got := callEdges(ix); got != 5 {
		t.Fatalf("callEdges = %d, want 5 (4 caller keys but 5 directed edges)", got)
	}
	out := indexSection(ix)
	if !strings.Contains(out, "Call edges: 5") {
		t.Errorf("indexSection should report 5 call edges, got %q", out)
	}
}
