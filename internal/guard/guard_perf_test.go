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

	start := time.Now()
	_, _ = CheckBoundariesPrecise(ix, b, files, false)
	elapsed := time.Since(start)
	if elapsed > 150*time.Millisecond {
		t.Fatalf("CheckBoundariesPrecise on 20k symbols / 30k edges took %s (budget 150ms) — the full-meta scan is back", elapsed)
	}
}
