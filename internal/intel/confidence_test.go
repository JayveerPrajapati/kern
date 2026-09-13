package intel

import (
	"strings"
	"testing"
)

// TestMinConfidenceFilter: tier ordering is EXTRACTED > INFERRED > AMBIGUOUS;
// the threshold accepts standard labels and internal tiers, case-insensitive;
// empty/unrecognized thresholds accept everything (filter is opt-in).
func TestMinConfidenceFilter(t *testing.T) {
	cases := []struct {
		min       string
		extracted bool // does EXTRACTED pass
		inferred  bool
		ambiguous bool
	}{
		{"", true, true, true},
		{"AMBIGUOUS", true, true, true},
		{"ambiguous", true, true, true},
		{"INFERRED", true, true, false},
		{"EXTRACTED", true, false, false},
		{"extracted", true, false, false},
		{"high", true, false, false},
		{"medium", true, true, false},
		{"low", true, true, true},
		{"BOGUS", true, true, true}, // unrecognized: opt-in, never silent default
	}
	for _, c := range cases {
		passes := MinConfidenceFilter(c.min)
		if got := passes(confExtracted); got != c.extracted {
			t.Errorf("min=%q: EXTRACTED pass = %v, want %v", c.min, got, c.extracted)
		}
		if got := passes(confInferred); got != c.inferred {
			t.Errorf("min=%q: INFERRED pass = %v, want %v", c.min, got, c.inferred)
		}
		if got := passes(confAmbiguous); got != c.ambiguous {
			t.Errorf("min=%q: AMBIGUOUS pass = %v, want %v", c.min, got, c.ambiguous)
		}
	}
}

// TestEdgeConfidenceLabel: cross-package Go direct calls are EXTRACTED (the
// parser records HIGH even though the directory heuristic would say
// cross-package), and a Java call resolved through a single-letter parameter
// is INFERRED (MEDIUM).
func TestEdgeConfidenceLabel(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": "package lib\n\nfunc Public() {}\n",
		"web/web.go": "package web\n\nimport \"demo/lib\"\n\nfunc Use() { lib.Public() }\n",
		"store/Store.java": `package store;
public interface Store {
	String fetch();
}
`,
		"store/App.java": `package store;
public class App {
	public String run(Store s) { return s.fetch(); }
}
`,
	})
	ix := buildIndex(t, dir)
	if got := EdgeConfidenceLabel(ix, "Use", "lib.Public"); got != confExtracted {
		t.Errorf("Go cross-package label = %s; want EXTRACTED", got)
	}
	// Java param-resolved call: the resolver tags type-inference rewrites
	// MEDIUM in both extractor paths (java_resolve.go, treesitter_java.go),
	// so the label is INFERRED under every build.
	if got := EdgeConfidenceLabel(ix, "App.run", "Store.fetch"); got != confInferred {
		t.Errorf("Java param-resolved label = %s; want INFERRED", got)
	}
}

// TestExploreMinPrunesAmbiguous: ExploreMin with minConf=INFERRED drops the
// LOW-confidence virtual-dispatch callee while the unfiltered Explore keeps
// it — and RenderExplore labels every row with its provenance. (Callees are
// rendered as simple names, so "FileStore.fetch" and "Store.fetch" collapse
// to "fetch"; the surviving row's label shows which tier won.)
func TestExploreMinPrunesAmbiguous(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"store/Store.java": `package store;
public interface Store {
	String fetch();
}
`,
		"store/FileStore.java": `package store;
public class FileStore implements Store {
	public String fetch() { return "x"; }
}
`,
		"store/App.java": `package store;
public class App {
	public String run(Store s) { return s.fetch(); }
}
`,
	})
	ix := buildIndex(t, dir)

	full, err := Explore(ix, "App.run", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(full.Callees, "fetch") {
		t.Fatalf("unfiltered explore missing virtual callee: %v", full.Callees)
	}
	if full.CalleeConf["fetch"] != confAmbiguous {
		t.Errorf("virtual callee conf = %q; want AMBIGUOUS", full.CalleeConf["fetch"])
	}
	out := RenderExplore(full)
	if !strings.Contains(out, "fetch [AMBIGUOUS]") {
		t.Errorf("render missing labeled virtual callee:\n%s", out)
	}

	filtered, err := ExploreMin(ix, "App.run", 0, 0, "EXTRACTED")
	if err != nil {
		t.Fatal(err)
	}
	if contains(filtered.Callees, "fetch") {
		t.Errorf("min-confidence explore kept inferred/virtual callee: %v", filtered.Callees)
	}
}

// TestGraphCtxMinPrunesAmbiguous: GraphCtxMin with minConf=EXTRACTED drops
// the INFERRED Java edge and the AMBIGUOUS virtual edge; the unfiltered
// GraphCtx shows both.
func TestGraphCtxMinPrunesAmbiguous(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"store/Store.java": `package store;
public interface Store {
	String fetch();
}
`,
		"store/FileStore.java": `package store;
public class FileStore implements Store {
	public String fetch() { return "x"; }
}
`,
		"store/App.java": `package store;
public class App {
	public String run(Store s) { return s.fetch(); }
}
`,
	})
	ix := buildIndex(t, dir)

	full, err := GraphCtx(ix, "App.run", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(full, "[INFERRED]") {
		t.Errorf("full graphctx missing INFERRED edge:\n%s", full)
	}
	filtered, err := GraphCtxMin(ix, "App.run", 0, "EXTRACTED")
	if err != nil {
		t.Fatal(err)
	}
	// Edge rows carry "[LABEL]"; the community line may still name members.
	if strings.Contains(filtered, "fetch [") {
		t.Errorf("min-confidence graphctx kept inferred/virtual edge rows:\n%s", filtered)
	}
}

// TestShortestPathMinSkipsLowEdges: the only path between App.run and
// FileStore.fetch runs through the LOW virtual edge; with minConf=EXTRACTED
// no path may exist, while the unfiltered search finds it.
func TestShortestPathMinSkipsLowEdges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"store/Store.java": `package store;
public interface Store {
	String fetch();
}
`,
		"store/FileStore.java": `package store;
public class FileStore implements Store {
	public String fetch() { return "x"; }
}
`,
		"store/App.java": `package store;
public class App {
	public String run(Store s) { return s.fetch(); }
}
`,
	})
	ix := buildIndex(t, dir)

	full := ShortestPath(ix, "App.run", "FileStore.fetch")
	if len(full) == 0 {
		t.Fatal("unfiltered path between App.run and FileStore.fetch not found")
	}
	if got := ShortestPathMin(ix, "App.run", "FileStore.fetch", "EXTRACTED"); len(got) != 0 {
		t.Errorf("min-confidence path should not traverse the LOW virtual edge: %v", got)
	}
}

// TestRenderPathLabelsHops: every hop of a rendered path carries its
// provenance label, so each step is FACT/INFERENCE-classifiable.
func TestRenderPathLabelsHops(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)
	path := ShortestPath(ix, "Caller", "Public")
	if len(path) == 0 {
		t.Fatal("path Caller -> Public not found")
	}
	out := RenderPath(ix, path)
	if !strings.Contains(out, "[EXTRACTED]") {
		t.Errorf("rendered path missing labels:\n%s", out)
	}
}

// TestWhyCarriesCallerConfidence: Why tags each caller with the provenance
// label of its edge, and FormatWhy renders it.
func TestWhyCarriesCallerConfidence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":       srcLib,
		"client/client.go": srcClient,
	})
	ix := buildIndex(t, dir)
	info, ok := Why(ix, "Public")
	if !ok {
		t.Fatal("Why(Public) failed")
	}
	if len(info.Callers) == 0 {
		t.Fatal("expected callers")
	}
	for _, c := range info.Callers {
		if c.Confidence == "" {
			t.Errorf("caller %s missing confidence label", c.Name)
		}
	}
	out := FormatWhy(info)
	if !strings.Contains(out, "[EXTRACTED]") {
		t.Errorf("why render missing labels:\n%s", out)
	}
}
