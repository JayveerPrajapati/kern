package intel

// impact_budget_test.go is the perf regression guard for the impact query
// sequence: the exact set of graph queries TaskService.Impact runs
// (collectGraphImpact's six queries plus ProductionCriticality via
// classifyCriticality) must complete well under a time budget on a hub
// symbol. Before the closure memo, the reverse closure was re-walked per
// query (WhatAPIsAffected, WhatServicesAffected, ProductionCriticality) and
// the forward closure per name query — the "#1 bypass trigger" for agents.
//
// The budget is configurable via KERN_IMPACT_BUDGET_MS (default 5000ms) and
// the test is skipped under -short because it builds an index fixture.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// impactBudgetMS reads the impact-query latency budget from
// KERN_IMPACT_BUDGET_MS (milliseconds). Unset or invalid values fall back to
// the documented default of 5000ms — generous for a CI box; the pre-memo
// sequence re-walked each transitive closure per query.
func impactBudgetMS(t *testing.T) time.Duration {
	t.Helper()
	ms := 5000
	if v := os.Getenv("KERN_IMPACT_BUDGET_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ms = n
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// impactHubFixture writes a synthetic hub package: Hub with 200 direct
// callers and a caller chain, so the reverse closure is a 200-node fan plus
// chain links and the forward closure is non-trivial (Hub calls Callee1).
func impactHubFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("package hub\n\n")
	b.WriteString("func Hub() { Callee1() }\n")
	b.WriteString("func Callee1() {}\n")
	b.WriteString("func Callee2() {}\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "func Caller%d() { Hub(); Callee2() }\n", i)
		if i > 0 {
			fmt.Fprintf(&b, "func Chain%d() { Caller%d() }\n", i, i)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hub.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestImpactQuerySequenceBudget runs the exact impact query sequence over a
// hub symbol and asserts the average stays under the configured budget. The
// first iteration primes lazy graph caches; measured iterations exercise the
// memoized closure path.
func TestImpactQuerySequenceBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an index fixture; skipped with -short")
	}
	ix, err := index.Build(impactHubFixture(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := FromIndex(ix)
	target := "Hub"

	run := func() {
		g.WhoCallsPrecise(target, false)
		g.WhatDoesXDependOnNames(target, false)
		g.DirectDependsOnNames(target, false)
		g.WhatServicesAffectedPrecise(target, false)
		g.WhatAPIsAffectedPrecise(target, false)
		g.WhatEventsAffectedPrecise(target, false)
		g.WhatTestsCoverPrecise(target, false)
		g.WhatDependsOnPrecise(target, false)
		g.ProductionCriticalityPrecise(target, false)
	}

	run() // 1 unmeasured warm-up iteration (primes lazy graph caches + memo)

	const iters = 10
	start := time.Now()
	for i := 0; i < iters; i++ {
		run()
	}
	avg := time.Since(start) / iters
	budget := impactBudgetMS(t)
	t.Logf("impact query sequence: %d iters, avg %v, budget %v", iters, avg, budget)
	if avg > budget {
		t.Errorf("impact query sequence avg %v exceeds budget %v", avg, budget)
	}
}
