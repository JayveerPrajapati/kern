package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// surprisingFixture builds a graph with three single-package-caller
// connectors and one true bridge:
//
//	a-clique:  A1->A2, A2->A3, A3->A4, A4->A1, A5<->A6
//	m-clique:  M1->M2, M2->M3, M3->M4, M4->M2
//	b-clique:  B1->B3, B2->B3, B3->B4, B4->B1
//	s-pair:    S<->S2
//
//	cross edges: A1->m.M1, A5->m.M1   (callers all in package a -> M1 is NOT a
//	             bridge, only 1 calling package)
//	             A2->b.B2, A5->b.B2   (callers all in package a -> B2 NOT a bridge)
//	             M1->b.B1, M3->b.B1   (callers all in package m -> B1 NOT a bridge)
//	             A3->s.S,  M2->s.S    (callers in a AND m -> S IS a bridge)
//
// The tests pass explicit labels, so the assertions do not depend on label
// propagation's outcome (that clustering is covered by index tests); they
// pin the ranking core: cross-community edges, distance x rarity scoring,
// bridge dedupe, deterministic ordering.
func surprisingFixture(t *testing.T) *index.Index {
	t.Helper()
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": `package a

import (
	"demo/b"
	"demo/m"
	"demo/s"
)

func A1() { A2(); m.M1() }
func A2() { A3(); b.B2() }
func A3() { A4(); s.S() }
func A4() { A1() }
func A5() { A6() }
func A6() { A5() }
`,
		"m/m.go": `package m

import (
	"demo/b"
	"demo/s"
)

func M1() { M2() }
func M2() { M3(); s.S() }
func M3() { M4(); b.B1() }
func M4() { M2() }
`,
		"b/b.go": `package b

func B1() { B3() }
func B2() { B3() }
func B3() { B4() }
func B4() { B3() }
`,
		"s/s.go": `package s

func S() { S2() }
func S2() { S() }
`,
	})
	return buildIndex(t, dir)
}

// explicitLabels names every fixture symbol's community directly.
func explicitLabels(t *testing.T, ix *index.Index) map[string]string {
	t.Helper()
	labels := map[string]string{}
	for _, s := range ix.Symbols {
		switch s.Name {
		case "A1", "A2", "A3", "A4", "A5", "A6":
			labels[s.FullName()] = "a-cluster"
		case "M1", "M2", "M3", "M4":
			labels[s.FullName()] = "m-cluster"
		case "B1", "B2", "B3", "B4":
			labels[s.FullName()] = "b-cluster"
		case "S", "S2":
			labels[s.FullName()] = "s-cluster"
		}
	}
	return labels
}

func TestSurprisingConnectionsRanksAndDedupes(t *testing.T) {
	ix := surprisingFixture(t)
	labels := explicitLabels(t, ix)
	edges := rankSurprisingConnections(ix, labels, 10)
	if len(edges) != 3 {
		t.Fatalf("expected 3 surprising edges, got %d: %+v", len(edges), edges)
	}
	for _, e := range edges {
		if e.Callee == "S" {
			t.Fatalf("bridge S leaked into surprising connections: %+v", e)
		}
		// Every pair is 2 hops apart via the third community and has a
		// single raw edge: score 2.0.
		if e.Distance != 2 || e.Edges != 1 || e.Score != 2.0 {
			t.Errorf("%s→%s: dist=%d edges=%d score=%v, want 2/1/2.0",
				e.Caller, e.Callee, e.Distance, e.Edges, e.Score)
		}
	}
	// All scores tie at 2.0, so the sort falls through to caller order.
	if edges[0].Caller != "A1" || edges[0].Callee != "M1" {
		t.Errorf("edges[0] = %s→%s, want A1→M1", edges[0].Caller, edges[0].Callee)
	}
	if edges[1].Caller != "A2" || edges[1].Callee != "B2" {
		t.Errorf("edges[1] = %s→%s, want A2→B2", edges[1].Caller, edges[1].Callee)
	}
	if edges[2].Caller != "M3" || edges[2].Callee != "B1" {
		t.Errorf("edges[2] = %s→%s, want M3→B1", edges[2].Caller, edges[2].Callee)
	}
}

func TestSurprisingConnectionsDisconnectedPairRanksFirst(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": `package a

import "demo/b"

func A1() { A2(); b.B2() }
func A2() { A3() }
func A3() { A1() }
`,
		"b/b.go": `package b

func B1() { B3() }
func B2() { B3() }
func B3() { B1() }
`,
	})
	ix := buildIndex(t, dir)
	labels := map[string]string{}
	for _, s := range ix.Symbols {
		if strings.HasPrefix(s.Name, "A") {
			labels[s.FullName()] = "a-cluster"
		} else {
			labels[s.FullName()] = "b-cluster"
		}
	}
	edges := rankSurprisingConnections(ix, labels, 5)
	if len(edges) != 1 {
		t.Fatalf("expected 1 surprising edge, got %d: %+v", len(edges), edges)
	}
	e := edges[0]
	if e.Caller != "A1" || e.Callee != "B2" {
		t.Errorf("edge = %s→%s, want A1→B2", e.Caller, e.Callee)
	}
	if e.Distance != maxCommunityDistance {
		t.Errorf("distance = %d, want %d (no alternate path)", e.Distance, maxCommunityDistance)
	}
	if e.Score != float64(maxCommunityDistance) {
		t.Errorf("score = %v, want %v", e.Score, float64(maxCommunityDistance))
	}
}

func TestSurprisingConnectionsRarityDividesScore(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": `package a

import "demo/b"

func A1() { A2(); b.B1() }
func A2() { A3(); b.B2() }
func A3() { A1() }
`,
		"b/b.go": `package b

func B1() { B3() }
func B2() { B3() }
func B3() { B4() }
func B4() { B3() }
`,
	})
	ix := buildIndex(t, dir)
	labels := map[string]string{}
	for _, s := range ix.Symbols {
		if strings.HasPrefix(s.Name, "A") {
			labels[s.FullName()] = "a-cluster"
		} else {
			labels[s.FullName()] = "b-cluster"
		}
	}
	edges := rankSurprisingConnections(ix, labels, 5)
	// Two raw edges between A and B, no alternate path: score = 9/2.
	if len(edges) != 2 {
		t.Fatalf("expected 2 sample edges, got %d: %+v", len(edges), edges)
	}
	for _, e := range edges {
		if e.Edges != 2 {
			t.Errorf("edges = %d, want 2", e.Edges)
		}
		if e.Score != float64(maxCommunityDistance)/2 {
			t.Errorf("score = %v, want %v", e.Score, float64(maxCommunityDistance)/2)
		}
	}
}

func TestSurprisingConnectionsLimit(t *testing.T) {
	ix := surprisingFixture(t)
	labels := explicitLabels(t, ix)
	if edges := rankSurprisingConnections(ix, labels, 1); len(edges) != 1 {
		t.Fatalf("limit 1 returned %d edges", len(edges))
	}
	if edges := rankSurprisingConnections(ix, labels, 0); len(edges) != 3 {
		t.Fatalf("limit 0 (default) returned %d edges, want 3", len(edges))
	}
}

func TestSurprisingConnectionsNoEdges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": `package a

func A1() { A2() }
func A2() { A1() }
`,
	})
	ix := buildIndex(t, dir)
	if edges := SurprisingConnections(ix, 5); len(edges) != 0 {
		t.Fatalf("expected no surprising edges, got %+v", edges)
	}
}

func TestSurprisingConnectionsDeterministic(t *testing.T) {
	ix := surprisingFixture(t)
	labels := explicitLabels(t, ix)
	first := rankSurprisingConnections(ix, labels, 10)
	second := rankSurprisingConnections(ix, labels, 10)
	if len(first) != len(second) {
		t.Fatalf("run-to-run length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("run-to-run edge %d differs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestRenderSurprising(t *testing.T) {
	if out := RenderSurprising(nil); !strings.Contains(out, "no surprising") {
		t.Fatalf("empty render = %q", out)
	}
	ix := surprisingFixture(t)
	labels := explicitLabels(t, ix)
	out := RenderSurprising(rankSurprisingConnections(ix, labels, 5))
	for _, want := range []string{"surprising connections", "B2", "b/b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}
