package intel

// latency_budget_test.go makes the "sub-100ms index-backed query" claim
// measurable (design-spec gap item D1). It builds ONE index over a bounded
// source set (the real internal/index and internal/intel packages,
// copied into a temp root so both live under a single Build root), then
// measures only warm in-memory query latency:
//
//   - index build + graph construction happen once, outside any timing
//   - each query gets 1 unmeasured warm-up iteration, then 20 measured ones
//   - per-query p50/p95/max and an overall p95 are computed; the overall p95
//     must be below the budget from KERN_LATENCY_BUDGET_MS (default 100ms)
//
// Queries span four distinct operations: ranked symbol search (intel.
// RankedSearch), forward depends-on (WhatDoesXDependOn), reverse depends-on
// (WhatDependsOn), plus cheap graph read APIs (WhoCalls, WhatAPIsAffected,
// Resolvable).
//
// Stdlib only (plus the module's own index/intel packages under test).

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// latencyOnce guards the one-time index build so the fixture is constructed a
// single time per test binary, no matter how many queries iterate over it.
var (
	latencyOnce sync.Once
	latencyIX   *index.Index
	latencyG    *Graph
	latencyErr  error
)

// latencyTestGraph builds the bounded fixture index once and derives the
// canonical graph from it. Index build and graph construction are explicitly
// NOT part of the measured section (requirement: only query latency counts).
// A *Graph is returned because Graph embeds a sync.Once and must not be
// copied (copylocks).
func latencyTestGraph(t *testing.T) (*index.Index, *Graph) {
	t.Helper()
	latencyOnce.Do(func() {
		ix, err := index.Build(latencyFixtureRoot(t))
		if err != nil {
			latencyErr = fmt.Errorf("build fixture index: %w", err)
			return
		}
		if len(ix.Symbols) < 50 {
			latencyErr = fmt.Errorf("fixture index unexpectedly small (%d symbols); source copy likely failed", len(ix.Symbols))
			return
		}
		latencyIX = ix
		g := FromIndex(ix)
		latencyG = &g
	})
	if latencyErr != nil {
		t.Fatalf("latency fixture: %v", latencyErr)
	}
	return latencyIX, latencyG
}

// latencyFixtureRoot copies the real internal/index and internal/intel
// Go sources into a fresh temp root so one bounded index.Build can cover both
// packages without walking the whole repo. Working directory for a test
// binary is the package dir (internal/intel), hence "../index".
func latencyFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	copyGoFiles := func(srcDir, rel string) {
		t.Helper()
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			t.Fatalf("latency fixture: read %s: %v", srcDir, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
			if err != nil {
				t.Fatalf("latency fixture: read %s: %v", e.Name(), err)
			}
			if err := os.WriteFile(filepath.Join(root, rel, e.Name()), data, 0o644); err != nil {
				t.Fatalf("latency fixture: write %s: %v", e.Name(), err)
			}
		}
	}

	for _, rel := range []string{"index", "intel"} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatalf("latency fixture: mkdir %s: %v", rel, err)
		}
	}
	copyGoFiles(filepath.Join("..", "index"), "index")
	copyGoFiles(".", "intel")
	return root
}

// firstSymbolNodeID returns the graph node ID of the first symbol node named
// wantName (preferred; e.g. "Build" from internal/index), falling back to any
// symbol node so the test never hard-fails on symbol drift — the measured
// query path is identical either way.
func firstSymbolNodeID(t *testing.T, g *Graph, wantName string) string {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Symbol != nil && n.Symbol.Name == wantName {
			return n.ID
		}
	}
	for _, n := range g.Nodes {
		if n.Symbol != nil {
			return n.ID
		}
	}
	t.Fatal("latency fixture graph contains no symbol nodes")
	return ""
}

// percentile returns the p-th percentile (0..1) of durs using the nearest-rank
// method on a sorted copy. It is deterministic and works for tiny samples.
func percentile(durs []time.Duration, p float64) time.Duration {
	if len(durs) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), durs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[int(float64(len(sorted)-1)*p)]
}

// latencyBudgetMS reads the latency budget from KERN_LATENCY_BUDGET_MS
// (milliseconds). Unset or invalid values fall back to the documented default
// of 100ms.
func latencyBudgetMS(t *testing.T) time.Duration {
	t.Helper()
	ms := 100
	if v := os.Getenv("KERN_LATENCY_BUDGET_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ms = n
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// TestLatencyBudget measures warm index-backed query latency and asserts the
// overall p95 stays under the configured budget (default 100ms).
func TestLatencyBudget(t *testing.T) {
	ix, g := latencyTestGraph(t)
	target := firstSymbolNodeID(t, g, "Build")

	// Representative query set: 8 queries across 4 distinct operations.
	queries := []struct {
		name string
		run  func()
	}{
		{"RankedSearch(Build)", func() { RankedSearch(ix, "Build", 10) }},
		{"RankedSearch(NewServer)", func() { RankedSearch(ix, "NewServer", 10) }},
		{"RankedSearch(Load)", func() { RankedSearch(ix, "Load", 10) }},
		{fmt.Sprintf("WhatDoesXDependOn(%s)", target), func() { g.WhatDoesXDependOn(target) }},
		{fmt.Sprintf("WhatDependsOn(%s)", target), func() { g.WhatDependsOn(target) }},
		{fmt.Sprintf("WhoCalls(%s)", target), func() { g.WhoCalls(target) }},
		{fmt.Sprintf("WhatAPIsAffected(%s)", target), func() { g.WhatAPIsAffected(target) }},
		{fmt.Sprintf("Resolvable(%s)", target), func() { g.Resolvable(target) }},
	}

	const iters = 20
	rows := make([][]time.Duration, len(queries))
	var overall []time.Duration
	for qi, q := range queries {
		q.run() // 1 unmeasured warm-up iteration (primes lazy graph caches)

		durs := make([]time.Duration, 0, iters)
		for i := 0; i < iters; i++ {
			start := time.Now()
			q.run()
			durs = append(durs, time.Since(start))
		}
		rows[qi] = durs
		overall = append(overall, durs...)
	}

	budget := latencyBudgetMS(t)

	// Diagnostic table: query name, p50, p95, max.
	var b strings.Builder
	fmt.Fprintf(&b, "%-32s %12s %12s %12s\n", "query", "p50", "p95", "max")
	for qi, q := range queries {
		r := rows[qi]
		max := r[0]
		for _, d := range r[1:] {
			if d > max {
				max = d
			}
		}
		fmt.Fprintf(&b, "%-32s %12s %12s %12s\n", q.name, percentile(r, 0.50), percentile(r, 0.95), max)
	}
	overallP95 := percentile(overall, 0.95)
	overallMax := overall[0]
	for _, d := range overall[1:] {
		if d > overallMax {
			overallMax = d
		}
	}
	fmt.Fprintf(&b, "%-32s %12s %12s %12s\n", "OVERALL (all queries)", percentile(overall, 0.50), overallP95, overallMax)
	fmt.Fprintf(&b, "budget: %v (KERN_LATENCY_BUDGET_MS=%q, default 100ms when unset/invalid)\n",
		budget, os.Getenv("KERN_LATENCY_BUDGET_MS"))

	t.Logf("latency budget table (%d queries x %d measured iterations):\n%s", len(queries), iters, b.String())

	if overallP95 >= budget {
		t.Errorf("overall p95 %v exceeds latency budget %v\n%s", overallP95, budget, b.String())
	}
}
