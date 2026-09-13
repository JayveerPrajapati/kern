package intel

import (
	"strings"
	"testing"
)

func TestExploreCombinesSourceCallFlowBlastRadius(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix := buildIndex(t, dir)

	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resolved != "Public" {
		t.Errorf("resolved = %q; want Public", rep.Resolved)
	}
	// Source is present and verbatim.
	if !strings.Contains(rep.Source, "func Public() string") {
		t.Errorf("source missing definition:\n%s", rep.Source)
	}
	// Direct call flow: callers include Caller and Deep; callees include inner.
	if !contains(rep.Callers, "Caller") {
		t.Errorf("callers missing Caller: %v", rep.Callers)
	}
	if !contains(rep.Callers, "Deep") {
		t.Errorf("callers missing Deep: %v", rep.Callers)
	}
	if !contains(rep.Callees, "inner") {
		t.Errorf("callees missing inner: %v", rep.Callees)
	}
	// Blast radius: Client.Caller -> Public is a transitive caller.
	if !contains(rep.BlastRadius, "Caller") {
		t.Errorf("blast radius missing Caller: %v", rep.BlastRadius)
	}
	if !contains(rep.BlastFiles, "client/client.go") {
		t.Errorf("blast files missing client/client.go: %v", rep.BlastFiles)
	}
}

func TestExploreDepthCapsBlastRadius(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)

	// inner is called by Public, which is called by Deep and (cross-package)
	// by client.Caller. Depth 0 (unlimited) includes the transitive callers.
	rep, err := Explore(ix, "inner", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(rep.BlastRadius, "Caller") {
		t.Fatalf("unlimited blast radius missing Caller: %v", rep.BlastRadius)
	}
	// Depth 1 caps at direct callers (Public) only.
	rep1, err := Explore(ix, "inner", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if contains(rep1.BlastRadius, "Caller") {
		t.Errorf("depth-1 blast radius should exclude transitive Caller: %v", rep1.BlastRadius)
	}
	if !contains(rep1.BlastRadius, "Public") {
		t.Errorf("depth-1 blast radius should include direct caller Public: %v", rep1.BlastRadius)
	}
}

func TestExploreUnknownSymbol(t *testing.T) {
	dir := writeTree(t, map[string]string{"lib/lib.go": srcLib})
	ix := buildIndex(t, dir)
	_, err := Explore(ix, "DoesNotExist", 0, 0)
	if err == nil {
		t.Fatal("expected error for unknown symbol")
	}
}

func TestRenderExplore(t *testing.T) {
	dir := writeTree(t, map[string]string{"lib/lib.go": srcLib})
	ix := buildIndex(t, dir)
	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderExplore(rep)
	for _, want := range []string{"symbol:", "== callers", "== callees", "== blast radius", "== source", "func Public() string"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// TestDefaultExploreBounds pins the P2-8 promotion defaults: unset bounds
// (the flags zero values) resolve to 2 hops / 30 nodes, while an explicit
// --depth 0 keeps the uncapped radius.
func TestDefaultExploreBounds(t *testing.T) {
	for _, tc := range []struct{ depth, max, wantD, wantM int }{
		{-1, 0, 2, 30},
		{0, 0, 0, 30},
		{1, 10, 1, 10},
		{3, 0, 3, 30},
		{-1, 200, 2, 200},
	} {
		if d, m := DefaultExploreBounds(tc.depth, tc.max); d != tc.wantD || m != tc.wantM {
			t.Errorf("DefaultExploreBounds(%d,%d) = (%d,%d), want (%d,%d)",
				tc.depth, tc.max, d, m, tc.wantD, tc.wantM)
		}
	}
}

// TestExploreAlwaysPopulatesStats pins P2-8 "always show tokens-saved": the
// savings panel is populated even on the unbudgeted verbatim path (0% saved
// is stated, not omitted).
func TestExploreAlwaysPopulatesStats(t *testing.T) {
	dir := writeTree(t, map[string]string{"lib/lib.go": srcLib})
	ix := buildIndex(t, dir)
	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stats == nil {
		t.Fatal("unbudgeted explore must still populate TokenStats")
	}
	if rep.Stats.FullContext <= 0 || rep.Stats.CompactTokens != rep.Stats.FullContext || rep.Stats.SavingsPct != 0 {
		t.Errorf("verbatim stats must be full==compact with 0%% saved, got %+v", rep.Stats)
	}
	if out := RenderExplore(rep); !strings.Contains(out, "tokens: explore") {
		t.Errorf("render missing always-on savings panel:\n%s", out)
	}
}

// TestRenderExploreExplain pins P2-8 --explain: the report plus the
// why-rationale section in one call.
func TestRenderExploreExplain(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
		"lib/lib_test.go":  srcTest,
	})
	ix := buildIndex(t, dir)
	rep, err := Explore(ix, "Public", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	info, ok := Why(ix, "Public")
	if !ok {
		t.Fatal("Why(Public) failed")
	}
	out := RenderExploreExplain(rep, info)
	for _, want := range []string{"== why ==", "who depends on it and why", "Public"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain render missing %q:\n%s", want, out)
		}
	}
}
