package intel

import (
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/qualify"
)

// ambiguousCallersIndex models the silent-drop shape: two declarations share
// the bare name `resolve` in different files, and the index holds call edges
// recorded under that bare name. No caller can be attributed to either
// declaration, so explore must say so instead of reporting an empty caller set
// as if nothing called the symbol.
func ambiguousCallersIndex() *index.Index {
	return &index.Index{
		Root: "/densic",
		Symbols: []index.Symbol{
			sym("func", "resolve", "crates/densic-mcp/src/server.rs", 793),
			sym("func", "resolve", "crates/densic-query/src/resolve.rs", 102),
		},
		Callers: map[string][]string{
			"resolve": {"DensicServer.context", "DensicServer.explore", "DensicServer.context"},
		},
		UpdatedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC),
	}
}

func TestExploreReportsUnboundCallers(t *testing.T) {
	ix := ambiguousCallersIndex()
	rep, err := Explore(ix, "resolve", 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Callers) != 0 {
		t.Fatalf("no caller is attributable; got %v", rep.Callers)
	}
	if rep.UnboundCallers != 2 {
		t.Errorf("UnboundCallers = %d; want 2 distinct call sites", rep.UnboundCallers)
	}
	out := RenderExplore(rep)
	// Scope the check to the callers block: other sections legitimately print
	// "(none)" for this fixture (no callees, no blast radius beyond itself).
	start := strings.Index(out, "== callers")
	if start < 0 {
		t.Fatalf("no callers section:\n%s", out)
	}
	block := out[start:]
	if end := strings.Index(block, "\n=="); end >= 0 {
		block = block[:end]
	}
	if strings.Contains(block, "(none)") {
		t.Errorf("an empty caller list must not render as a bare (none):\n%s", block)
	}
	if !strings.Contains(block, "none attributed") || !strings.Contains(block, "2 call site(s)") {
		t.Errorf("render must explain the unattributed call sites:\n%s", block)
	}
}

// TestUnboundCallersSilentWhenNameIsUnique: a unique name keeps the plain
// behaviour — its callers either resolve or genuinely do not exist.
func TestUnboundCallersSilentWhenNameIsUnique(t *testing.T) {
	ix := &index.Index{
		Root:    "/densic",
		Symbols: []index.Symbol{sym("func", "resolve", "crates/a/src/lib.rs", 5)},
		Callers: map[string][]string{"resolve": {"someone"}},
	}
	if got := qualify.UnboundCallers(ix, ix.Symbols[0]); got != 0 {
		t.Errorf("unique name reported %d unbound callers; want 0", got)
	}
}
