//go:build !race

// Performance gates are excluded from -race builds: race instrumentation
// distorts the wall-clock and CPU budgets these tests are calibrated against.

package guard

import (
	"fmt"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestCheckBoundariesPreciseScalesLinearly pins the pre-commit architecture-check regression. Before it,
// resolveCallee scanned the whole 17k-symbol meta map for every qualified
// callee that missed the exact fast path (~28.8k edges on kern ≈ 4.9e8 map
// iterations), and the import loop rebuilt the directory map per import. This
// gate builds 20k symbols / 30k qualified call edges: a regression to the
// full-map scan blows far past the budget, while the precomputed simple-name
// index stays in the tens of milliseconds.
//
// The budget is asserted on CPU time when the platform exposes it: the check
// is ~40ms isolated but ~0.9s wall while 200+ parallel test binaries compete
// for 8 cores (audit-closure, 2026-11-03 — the full ./... pass failed on the
// wall-clock budget while the same code passed 3/3 isolated). CPU time is
// load-independent, so the gate keeps its 150ms signal; off unix it falls
// back to a relaxed wall-clock budget (best of 3) that still catches the
// full-meta-scan regression this gate exists for (seconds of work per run).
func TestCheckBoundariesPreciseScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("performance gate: skipped in -short")
	}
	const (
		dirs      = 100
		perDir    = 200 // 20k symbols
		edgesEach = 3   // 30k call edges
	)
	ix := &index.Index{
		Symbols:       make([]index.Symbol, 0, dirs*perDir),
		SymbolsByFile: map[string][]index.Symbol{},
		Calls:         map[string][]index.CallEdge{},
		Callers:       map[string][]string{},
		Pkgs:          map[string]*index.Pkg{},
	}
	for d := 0; d < dirs; d++ {
		dir := fmt.Sprintf("pkg%03d", d)
		file := dir + "/file.go"
		ix.Pkgs[dir] = &index.Pkg{}
		for s := 0; s < perDir; s++ {
			sym := index.Symbol{Name: fmt.Sprintf("Fn%03d_%03d", d, s), Kind: "func", File: file, Line: s + 1}
			ix.Symbols = append(ix.Symbols, sym)
			ix.SymbolsByFile[file] = append(ix.SymbolsByFile[file], sym)
			// Three qualified callees into other package dirs whose simple
			// name exists but whose qualified form is not an exact meta key.
			for e := 1; e <= edgesEach; e++ {
				to := (d + e) % dirs
				ix.Calls[sym.FullName()] = append(ix.Calls[sym.FullName()], index.CallEdge{
					Target: fmt.Sprintf("pkg%03d.Fn%03d_%03d", to, to, s),
				})
			}
		}
	}
	files := make([]string, 0, dirs)
	for d := 0; d < dirs; d++ {
		files = append(files, fmt.Sprintf("pkg%03d/file.go", d))
	}
	b := &Boundaries{Rules: []domain.BoundaryRule{{
		From: "app", To: "infra", Action: "forbid",
	}}}

	// Best of 3 on both clocks: a single sample can straddle a GC assist, and
	// the regression under test is uniform across runs.
	bestWall, bestCPU := time.Duration(1<<62-1), time.Duration(1<<62-1)
	cpuAvailable := false
	for i := 0; i < 3; i++ {
		cpuBefore, ok := processCPUTime()
		cpuAvailable = ok
		start := time.Now()
		_, _ = CheckBoundariesPrecise(ix, b, files, false)
		if elapsed := time.Since(start); elapsed < bestWall {
			bestWall = elapsed
		}
		if cpuAfter, ok := processCPUTime(); ok {
			if spent := cpuAfter - cpuBefore; spent < bestCPU {
				bestCPU = spent
			}
		}
	}
	if cpuAvailable {
		if bestCPU > 150*time.Millisecond {
			t.Fatalf("CheckBoundariesPrecise on 20k symbols / 30k edges burned %s CPU (best of 3; budget 150ms) — the full-meta scan is back", bestCPU)
		}
		return
	}
	if bestWall > 1500*time.Millisecond {
		t.Fatalf("CheckBoundariesPrecise on 20k symbols / 30k edges took %s wall (best of 3; relaxed budget 1500ms off unix) — the full-meta scan is back", bestWall)
	}
}
