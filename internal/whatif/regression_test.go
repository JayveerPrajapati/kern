package whatif

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// f3FixtureGraph hand-crafts a graph shaped like the F3 regression: a hub
// symbol (Bus) that, under the old unbounded WhatTestsCover expansion, every
// test in the fixture would transitively reach (t1/t2/t3 -> a.A/b.B -> Bus),
// plus one same-package test (bus.TestBus) and one direct cross-package
// caller test (busx.TestBusDirect). Scoped attribution must report only the
// directly-relevant tests, and the rename kind path must stay fast.
func f3FixtureGraph() *intel.Graph {
	g := &intel.Graph{}
	sym := func(id, name, file string) domain.Node {
		return domain.Node{
			ID:     id,
			Kind:   "symbol",
			Label:  id,
			Symbol: &domain.Symbol{Name: name, Qualified: id, File: file},
		}
	}
	g.Nodes = []domain.Node{
		sym("bus.Bus", "Bus", "bus/bus.go"),
		sym("bus.TestBus", "TestBus", "bus/bus_test.go"),
		sym("busx.TestBusDirect", "TestBusDirect", "busx/busx_test.go"),
		sym("a.A", "A", "a/a.go"),
		sym("b.B", "B", "b/b.go"),
		sym("t1.TestT1", "TestT1", "t1/t1_test.go"),
		sym("t2.TestT2", "TestT2", "t2/t2_test.go"),
		sym("t3.TestT3", "TestT3", "t3/t3_test.go"),
	}
	g.Edges = []domain.Edge{
		{From: "a.A", To: "b.B", Kind: "calls"},
		{From: "b.B", To: "bus.Bus", Kind: "calls"},
		{From: "busx.TestBusDirect", To: "bus.Bus", Kind: "calls"},
		{From: "t1.TestT1", To: "a.A", Kind: "calls"},
		{From: "t2.TestT2", To: "b.B", Kind: "calls"},
		{From: "t3.TestT3", To: "a.A", Kind: "calls"},
	}
	return g
}

// TestWhatIfTestAttributionBounded is a deterministic fixture regression for
// F3: a hub symbol must report a bounded set of directly-relevant covering
// tests (same package + direct callers), never every test that transitively
// reaches it, and the rename kind path must complete quickly.
func TestWhatIfTestAttributionBounded(t *testing.T) {
	g := f3FixtureGraph()
	start := time.Now()
	imp := Simulate(g, Change{Kind: RenameSymbol, Target: "bus.Bus", NewTarget: "bus.BusTask"})
	elapsed := time.Since(start)
	// The kind path must be fast — it was ~5 minutes on a hub symbol before
	// F3. The fixture graph is tiny, so a generous bound only catches a
	// pathological regression (e.g. a per-call full-suite scan).
	if elapsed > 5*time.Second {
		t.Fatalf("rename kind path took %v; F3 regression (want <5s)", elapsed)
	}
	// Only the same-package test and the direct caller test cover the hub.
	if len(imp.Tests) != 2 {
		t.Fatalf("Simulate(rename Bus).Tests = %v, want exactly [bus.TestBus busx.TestBusDirect] (bounded, not all fixture tests)", imp.Tests)
	}
	for _, want := range []string{"bus.TestBus", "busx.TestBusDirect"} {
		if !contains(imp.Tests, want) {
			t.Errorf("covering tests missing %q; got %v", want, imp.Tests)
		}
	}
	// The unrelated transitive-only tests must NOT be attributed.
	for _, unwanted := range []string{"t1.TestT1", "t2.TestT2", "t3.TestT3"} {
		if contains(imp.Tests, unwanted) {
			t.Errorf("transitive-only test %q wrongly attributed as covering the hub; got %v", unwanted, imp.Tests)
		}
	}
}

// repoRoot returns the kern repository root (three levels above this test
// file: internal/whatif -> internal -> repo root).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// isTestSymbol mirrors intel.isTest for counting purposes.
func isTestSymbol(s *domain.Symbol) bool {
	if s == nil {
		return false
	}
	return strings.HasPrefix(s.Name, "Test") || strings.Contains(s.File, "_test.go")
}

// TestWhatIfKindPathFastOnRepo is the live-repo regression for F3: on the kern
// repo itself, the eventbus hub symbol must report a bounded test set and the
// rename kind path must complete quickly (it was 287.98s with 3051 tests).
// Skips when the repo index is unavailable (e.g. a fresh checkout without a
// persisted index), so the suite stays green outside this repo.
func TestWhatIfKindPathFastOnRepo(t *testing.T) {
	root := repoRoot(t)
	ix, err := index.Load(root)
	if err != nil {
		t.Skipf("no persisted index at %s (skip F3 live regression): %v", root, err)
	}
	g := intel.FromIndex(ix)
	// The hub symbol from the finding: Bus.enqueueDeadLetter (internal/eventbus).
	hub := ""
	for _, n := range g.Nodes {
		if n.Symbol != nil && n.Symbol.Name == "enqueueDeadLetter" {
			hub = n.ID
			break
		}
	}
	if hub == "" {
		t.Skipf("hub symbol enqueueDeadLetter not in index (skip F3 live regression)")
	}
	totalTests := 0
	for _, n := range g.Nodes {
		if isTestSymbol(n.Symbol) {
			totalTests++
		}
	}
	start := time.Now()
	imp := Simulate(&g, Change{Kind: RenameSymbol, Target: hub, NewTarget: hub + "Task"})
	elapsed := time.Since(start)
	if elapsed > 30*time.Second {
		t.Fatalf("rename kind path on %s took %v (>30s); F3 regression (was ~288s)", hub, elapsed)
	}
	// Scoped attribution: a handful, not the whole repo's test suite.
	if len(imp.Tests) > 200 {
		t.Fatalf("WhatTestsCover(%s) = %d tests; F3 regression (was ~3051)", hub, len(imp.Tests))
	}
	if totalTests > 0 && len(imp.Tests) >= totalTests {
		t.Fatalf("test attribution not scoped: %d covering tests >= %d total tests", len(imp.Tests), totalTests)
	}
}
