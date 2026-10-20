package topology

import (
	"strings"
	"testing"
)

// The tests in this file moved from internal/intel/callee_noise_test.go with
// the community/surprising family (they exercise the unexported
// renderCommunities / rankSurprisingConnections cores).

// TestRenderCommunitiesIDSkipsTestFiles pins the community-ID guard: the
// first project-local symbol becomes the community ID, but a test-file symbol
// must not — a Test*-named community would pollute digest/graph output. The
// guard covers stale indexes whose clustering still contains test symbols.
func TestRenderCommunitiesIDSkipsTestFiles(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

func produce() { consume() }
func consume() {}
`,
		"a/a_test.go": `package a

func TestProduceConsume() { produce() }
`,
	})
	ix := buildIndex(t, dir)
	// Explicit labels including the test symbol (a stale index's clustering).
	labels := map[string]string{
		"produce": "g", "consume": "g", "TestProduceConsume": "g",
	}
	comms := renderCommunities(ix, declarationTestLabels(ix, labels))
	if len(comms) != 1 {
		t.Fatalf("expected one community, got %+v", comms)
	}
	if strings.HasPrefix(comms[0].ID, "Test") {
		t.Errorf("community ID %q is a test symbol; must pick a production symbol", comms[0].ID)
	}
	if comms[0].ID != "consume" {
		t.Errorf("community ID = %q, want consume (first production symbol in sorted order)", comms[0].ID)
	}
}

// TestSurprisingSkipsTestCallees pins the callee-side test filter: a
// production caller invoking a test function (TestKillKillsWholeGroup shape)
// must not rank as a surprising connection — the caller side was already
// filtered, the callee side leaked before this fix.
func TestSurprisingSkipsTestCallees(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": `package a

import "demo/b"

func A1() { b.B2(); TestProduce() }
`,
		"a/a_test.go": `package a

func TestProduce() {}
`,
		"b/b.go": `package b

func B2() {}
`,
	})
	ix := buildIndex(t, dir)
	labels := map[string]string{
		"A1": "a-cluster", "B2": "b-cluster", "TestProduce": "test-cluster",
	}
	edges := rankSurprisingConnections(ix, declarationTestLabels(ix, labels), 5)
	for _, e := range edges {
		if strings.HasPrefix(e.Callee, "Test") {
			t.Fatalf("test callee %q leaked into surprising connections: %+v", e.Callee, edges)
		}
	}
	// Exactly the production cross-community edge survives: A1 -> B2. The
	// test callee edge (A1 -> TestProduce) is filtered, and TestProduce is
	// called from one package so it is not a bridge — without the callee
	// filter it would rank.
	if len(edges) != 1 || edges[0].Caller != "A1" || edges[0].Callee != "B2" {
		t.Fatalf("surprising edges = %+v, want exactly [A1 -> B2]", edges)
	}
}
