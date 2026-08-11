package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestResolveSymbol(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"Caller": "Caller",
		"inner":  "inner",
		"Deep":   "Deep",
		"nope":   "",
	}
	for in, want := range cases {
		got, ok := Resolve(ix, in)
		if want == "" {
			if ok {
				t.Errorf("Resolve(%q) should fail, got %q", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestShortestPath(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Caller -> Public -> inner
	path := ShortestPath(ix, "Caller", "inner")
	if len(path) != 3 || path[0] != "Caller" || path[1] != "Public" || path[2] != "inner" {
		t.Errorf("expected [Caller Public inner], got %v", path)
	}
	// Same-symbol path is trivial.
	if p := ShortestPath(ix, "Caller", "Caller"); len(p) != 1 || p[0] != "Caller" {
		t.Errorf("expected [Caller], got %v", p)
	}
	// Disconnected symbols: LocalOnly is never called and calls nothing.
	if p := ShortestPath(ix, "Caller", "LocalOnly"); p != nil {
		t.Errorf("expected no path to LocalOnly, got %v", p)
	}
}

func TestDeadCodeFindsUncalled(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	dead := DeadCode(ix)
	found := map[string]bool{}
	for _, d := range dead {
		found[d.Name] = true
	}
	if !found["Deep"] {
		t.Errorf("Deep is never called and should be dead, got %v", found)
	}
	if !found["LocalOnly"] {
		t.Errorf("LocalOnly is never called and should be dead, got %v", found)
	}
	// Caller has no callers anywhere in this fixture (public, but no internal
	// callers) so it is reported dead as a public-API symbol.
	if !found["Caller"] {
		t.Errorf("Caller has no callers and should be flagged, got %v", found)
	}
	if found["Public"] || found["inner"] || found["UntestedHot"] {
		t.Errorf("called symbols must not be reported dead: %v", found)
	}
	// Public entry is reported with the external-API tag.
	for _, d := range dead {
		if d.Name == "Caller" && !d.Public {
			t.Errorf("Caller is an exported name and should be marked public")
		}
	}
}

func TestLargeFunctionsThreshold(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	large := LargeFunctions(ix, 3)
	names := map[string]bool{}
	for _, l := range large {
		names[l.Name] = true
	}
	// Public spans 4 lines; LocalOnly is a one-liner.
	if !names["Public"] {
		t.Errorf("Public is 4 lines and should qualify, got %v", names)
	}
	if names["LocalOnly"] {
		t.Errorf("LocalOnly is 1 line and must not qualify")
	}
	// Sorted by size descending.
	for i := 1; i < len(large); i++ {
		if large[i].Lines > large[i-1].Lines {
			t.Fatalf("not sorted by size: %+v", large)
		}
	}
}

func TestArchitectureOverview(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := AnalyzeArchitecture(ix)
	if len(a.Communities) == 0 {
		t.Fatal("expected at least one community in the architecture view")
	}
	big := a.Communities[0]
	if big.Size < 3 {
		t.Errorf("expected the main community to span the call graph, got %+v", big)
	}
}

func TestParseLogCountsPerCommit(t *testing.T) {
	out := "a.go\nb.go\n\nb.go\nc.go\n\na.go\n"
	counts, commits := parseLog(out)
	if counts["a.go"] != 2 || counts["b.go"] != 2 || counts["c.go"] != 1 {
		t.Errorf("parseLog miscounted: %v", counts)
	}
	if commits != 3 {
		t.Errorf("parseLog commit count = %d; want 3", commits)
	}
}
