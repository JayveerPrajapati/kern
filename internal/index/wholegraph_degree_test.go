package index

import "testing"

// TestWholeGraphDegreeCountsResolvedOnly pins B5 (deep-dive 2026-10-03):
// the whole-graph ranking degree must count only edges whose endpoint
// resolves to an indexed symbol — the same edges the result can emit.
// Previously len(ix.Calls[id]) + len(ix.Callers[id]) counted unresolved
// external targets (assert.Equal, fluent fragments), so a symbol whose
// bucket was full of unresolvable noise outranked a genuinely connected
// one, and the ranking described a different graph than the emitted edges.
func TestWholeGraphDegreeCountsResolvedOnly(t *testing.T) {
	ix := &Index{
		Root: ".",
		Symbols: []Symbol{
			{Name: "Noisy", Kind: "func", File: "noisy.go", Lang: "go"},
			{Name: "Wired", Kind: "func", File: "wired.go", Lang: "go"},
			{Name: "Peer", Kind: "func", File: "peer.go", Lang: "go"},
			{Name: "Zeta", Kind: "func", File: "zeta.go", Lang: "go"},
		},
		Calls: map[string][]CallEdge{
			// Noisy's bucket: only unresolvable external targets.
			"Noisy": {
				{Target: "assert.Equal"}, {Target: "fmt.Sprintf"},
				{Target: ".Str.Int.Msg"},
			},
			// Wired: one real resolved callee.
			"Wired": {{Target: "Peer"}},
		},
		Callers: map[string][]string{
			"Peer":  {"Wired"},
			"Wired": {"Zeta"},
		},
	}
	// Raw-degree ranking would put Noisy (3) above Wired (2). With
	// resolved-only degree Noisy is 0, Wired 2, Peer/Zeta 1 — a limit-2
	// whole graph must keep Wired and Peer (dropping Noisy) and emit the
	// Wired→Peer edge.
	g := ix.WholeGraph(2)
	if len(g.Nodes) != 2 {
		t.Fatalf("expected exactly 2 nodes, got %d", len(g.Nodes))
	}
	wired, peer := false, false
	for _, n := range g.Nodes {
		if n.Name == "Noisy" {
			t.Fatalf("Noisy (unresolved-only bucket) must not survive the degree cut")
		}
		if n.Name == "Wired" {
			wired = true
		}
		if n.Name == "Peer" {
			peer = true
		}
	}
	if !wired || !peer {
		t.Fatalf("Wired and Peer must be kept; nodes: %+v", g.Nodes)
	}
	for _, e := range g.Edges {
		if e.From == "Wired" && e.To == "Peer" {
			return // pass
		}
	}
	t.Fatalf("expected the Wired→Peer edge, got %+v", g.Edges)
}
