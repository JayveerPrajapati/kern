package highlevel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/servedledger"
)

// planTestPacket builds a context packet with two controlled affected files:
// boundary.go (0 outbound deps, 2 symbols, 90 LOC — the cheap first edit) and
// server.go (1 outbound dep, 2 symbols, 1800 LOC).
func planTestPacket() *domain.ContextPacket {
	return &domain.ContextPacket{
		Files: []domain.File{
			{Path: "internal/domain/boundary.go", Lines: 90, Symbols: []domain.Symbol{
				{Name: "SafetyBudget", Qualified: "domain.SafetyBudget", Kind: "type"},
				{Name: "TrackToolCall", Qualified: "domain.SafetyBudget.TrackToolCall", Kind: "method"},
			}},
			{Path: "internal/mcp/server.go", Lines: 1800, Symbols: []domain.Symbol{
				{Name: "Server", Qualified: "mcp.Server", Kind: "type"},
				{Name: "runTool", Qualified: "mcp.Server.runTool", Kind: "method"},
			}},
		},
		Dependencies: []domain.Edge{
			{From: "mcp.Server.runTool", To: "domain.SafetyBudget", Kind: "calls"},
		},
	}
}

// TestPlanExtensionsSectionsAndVerdictFlip (B3): the plan output carries both
// sections; the context-sufficiency verdict flips as the session's ledger
// records coverage for the affected files — false with a missing list, then
// true with the missing line omitted once everything is served.
func TestPlanExtensionsSectionsAndVerdictFlip(t *testing.T) {
	pkt := planTestPacket()
	ledger := servedledger.New()
	h := Hooks{ServedThisSession: func(files []string) map[string]bool {
		return ledger.FilesCovered("default")
	}}
	args := map[string]any{"agent_id": "alice", "task": "t1"}
	base := "PLAN for: Greet\nplan text"

	// Nothing served yet: both sections present, verdict false, both files
	// missing, session label = the agent part of the key.
	out := appendPlanExtensions(base, h, args, pkt)
	for _, want := range []string{
		"## Context sufficiency",
		"ready-to-execute: false",
		"missing: internal/domain/boundary.go, internal/mcp/server.go",
		"served: 0/2 affected files (session alice)",
		"## First slice",
		"Smallest verifiable first edit: internal/domain/boundary.go (SafetyBudget)",
		"one type, verify with: go test ./internal/domain/",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan output must contain %q, got:\n%s", want, out)
		}
	}

	// Serve the first file: verdict still false, but only server.go missing.
	ledger.Record("default", "kern_explore", `{"symbol":"SafetyBudget"}`, "e", []string{"internal/domain/boundary.go"})
	out = appendPlanExtensions(base, h, args, pkt)
	if !strings.Contains(out, "ready-to-execute: false") || !strings.Contains(out, "missing: internal/mcp/server.go") {
		t.Fatalf("one missing file must keep the verdict false with only it listed, got:\n%s", out)
	}
	if strings.Contains(out, "missing: internal/domain/boundary.go") {
		t.Fatalf("a served file must leave the missing list, got:\n%s", out)
	}
	if !strings.Contains(out, "served: 1/2 affected files") {
		t.Fatalf("the served count must reflect the recorded coverage, got:\n%s", out)
	}

	// Serve the second file: verdict true, missing line omitted.
	ledger.Record("default", "kern_explore", `{"symbol":"Server"}`, "e", []string{"internal/mcp/server.go"})
	out = appendPlanExtensions(base, h, args, pkt)
	if !strings.Contains(out, "ready-to-execute: true") {
		t.Fatalf("all files served must flip the verdict to true, got:\n%s", out)
	}
	if strings.Contains(out, "missing:") {
		t.Fatalf("the missing line is omitted when nothing is missing, got:\n%s", out)
	}
	if !strings.Contains(out, "served: 2/2 affected files (session alice)") {
		t.Fatalf("the served line must show the full count, got:\n%s", out)
	}
}

// TestPlanExtensionsNoHookOmitsSufficiency: without the ServedThisSession
// hook there is no coverage data — the sufficiency section is omitted while
// the first slice (which needs no hook) still renders.
func TestPlanExtensionsNoHookOmitsSufficiency(t *testing.T) {
	pkt := planTestPacket()
	out := appendPlanExtensions("PLAN for: Greet\nplan text", Hooks{}, map[string]any{}, pkt)
	if strings.Contains(out, "## Context sufficiency") {
		t.Fatalf("a nil ServedThisSession hook must omit the sufficiency section, got:\n%s", out)
	}
	if !strings.Contains(out, "## First slice") {
		t.Fatalf("the first slice section must render without the hook, got:\n%s", out)
	}
}

// TestPlanExtensionsSessionDefaultLabel: without an agent_id the session
// label renders as "default".
func TestPlanExtensionsSessionDefaultLabel(t *testing.T) {
	pkt := planTestPacket()
	ledger := servedledger.New()
	h := Hooks{ServedThisSession: func(files []string) map[string]bool {
		return ledger.FilesCovered("default")
	}}
	out := appendPlanExtensions("PLAN for: Greet\nplan text", h, map[string]any{}, pkt)
	if !strings.Contains(out, "served: 0/2 affected files (session default)") {
		t.Fatalf("the default session label must render, got:\n%s", out)
	}
}

// TestRankFirstSliceGolden (B5): the first-slice ranking is deterministic —
// fewest outbound deps, then fewest symbols, then smallest LOC, then lexical
// — pinned on a fixed input including the lexical tiebreak.
func TestRankFirstSliceGolden(t *testing.T) {
	pkt := &domain.ContextPacket{
		Files: []domain.File{
			{Path: "internal/zeta.go", Lines: 100, Symbols: []domain.Symbol{{Name: "Zeta", Kind: "func"}}},
			{Path: "internal/beta.go", Lines: 60, Symbols: []domain.Symbol{{Name: "Beta", Kind: "type"}}},
			{Path: "internal/alpha.go", Lines: 60, Symbols: []domain.Symbol{{Name: "Alpha", Kind: "type"}}},
		},
		Dependencies: []domain.Edge{{From: "Zeta", To: "Alpha", Kind: "calls"}},
	}
	// zeta carries the only outbound dep; alpha and beta tie on every
	// dimension → the lexical tiebreak picks alpha.
	top, ok := rankFirstSlice(pkt)
	if !ok {
		t.Fatal("rankFirstSlice must rank a non-empty packet")
	}
	if top.Path != "internal/alpha.go" || top.Symbol != "Alpha" || top.Kind != "type" {
		t.Fatalf("rankFirstSlice = %+v, want alpha.go (Alpha, type)", top)
	}
	// Golden rendered section.
	got := renderFirstSlice(pkt)
	want := "## First slice\nSmallest verifiable first edit: internal/alpha.go (Alpha)\none type, verify with: go test ./internal/"
	if got != want {
		t.Fatalf("renderFirstSlice = %q, want %q", got, want)
	}
	// Golden full ordering, including the tiebreak.
	ranked := rankFiles(pkt.Files, outboundDeps(pkt))
	paths := make([]string, 0, len(ranked))
	for _, f := range ranked {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != "internal/alpha.go,internal/beta.go,internal/zeta.go" {
		t.Fatalf("rankFiles order = %v, want alpha,beta,zeta", paths)
	}
}

// TestRankFirstSliceFileWithoutSymbols: a file with no packet symbols still
// ranks (entry symbol absent → the file alone renders).
func TestRankFirstSliceFileWithoutSymbols(t *testing.T) {
	pkt := &domain.ContextPacket{
		Files: []domain.File{
			{Path: "internal/mcp/toolpolicy.go", Lines: 400},
		},
	}
	top, ok := rankFirstSlice(pkt)
	if !ok || top.Path != "internal/mcp/toolpolicy.go" || top.Symbol != "" {
		t.Fatalf("rankFirstSlice = %+v ok=%v, want toolpolicy.go with no symbol", top, ok)
	}
	got := renderFirstSlice(pkt)
	want := "## First slice\nSmallest verifiable first edit: internal/mcp/toolpolicy.go\nverify with: go test ./internal/mcp/"
	if got != want {
		t.Fatalf("renderFirstSlice = %q, want %q", got, want)
	}
}
