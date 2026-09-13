package index

import "testing"

// TestNeighborhoodUsesParserConfidence pins G-P0-1: Neighborhood must prefer
// the parser's per-edge confidence (CallEdge.Confidence, like WholeGraph)
// over the directory heuristic. Two fixtures where the two disagree:
//
//   - Go cross-directory call: parser says HIGH (direct call, goast.go),
//     the directory heuristic would say medium.
//   - Python same-directory call: the regex extractor resolves via
//     name-heuristic matching and says MEDIUM, the directory heuristic
//     would say high.
func TestNeighborhoodUsesParserConfidence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": "package lib\n\nfunc Public() {}\n",
		"web/web.go": "package web\n\nimport \"demo/lib\"\n\nfunc Use() { lib.Public() }\n",
		"app/app.py": "def helper():\n    pass\n\n\ndef main():\n    helper()\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Go: cross-directory edge must carry the parser's HIGH, not the
	// heuristic's medium. (The recorded qualified target "lib.Public" is
	// untouched — promoteLowEdges only reconciles LOW edges.)
	g, ok := ix.Neighborhood("Use")
	if !ok {
		t.Fatal("Neighborhood(Use) not found")
	}
	edge := findEdge(g, "Use", "lib.Public")
	if edge == nil {
		t.Fatalf("missing callee edge Use -> lib.Public: %+v", g.Edges)
	}
	if edge.Confidence != confHigh || edge.ConfidenceLabel != confExtracted {
		t.Errorf("Go cross-dir edge = %s/%s; want high/EXTRACTED (parser wins over heuristic)", edge.Confidence, edge.ConfidenceLabel)
	}

	// Python: same-directory edge must carry the parser's confidence, not
	// the directory heuristic. The parser tier differs by build: the regex
	// extractor resolves via name-heuristic matching (MEDIUM), the
	// tree-sitter extractor sees the call directly in the AST (HIGH). Both
	// are parser truth; the divergence itself is pinned by CG-P0-4.
	g2, ok := ix.Neighborhood("main")
	if !ok {
		t.Fatal("Neighborhood(main) not found")
	}
	wantPyConf := confMedium
	wantPyLabel := confInferred
	if treesitterEnabled() {
		wantPyConf = confHigh
		wantPyLabel = confExtracted
	}
	edge2 := findEdge(g2, "main", "helper")
	if edge2 == nil {
		t.Fatalf("missing callee edge main -> helper: %+v", g2.Edges)
	}
	if edge2.Confidence != wantPyConf || edge2.ConfidenceLabel != wantPyLabel {
		t.Errorf("Python same-dir edge = %s/%s; want %s/%s (parser wins over heuristic)",
			edge2.Confidence, edge2.ConfidenceLabel, wantPyConf, wantPyLabel)
	}
}

// TestNeighborhoodVirtualEdgeIsAmbiguous: interface dispatch edges are
// synthesized from the explicit inheritance graph (LOW) — Neighborhood must
// surface them as AMBIGUOUS so consumers can prune them. Go interfaces are
// satisfied structurally (no implements edges recorded), so the fixture uses
// Java where `implements` is explicit.
func TestNeighborhoodVirtualEdgeIsAmbiguous(t *testing.T) {
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
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, ok := ix.Neighborhood("App.run")
	if !ok {
		t.Fatal("Neighborhood(App.run) not found")
	}
	for _, e := range g.Edges {
		if e.To == "FileStore.fetch" {
			if e.Confidence != confLow || e.ConfidenceLabel != confAmbiguous {
				t.Errorf("virtual edge = %s/%s; want low/AMBIGUOUS", e.Confidence, e.ConfidenceLabel)
			}
			return
		}
	}
	t.Fatalf("virtual edge App.run -> FileStore.fetch missing: %+v", g.Edges)
}

func findEdge(g GraphResult, from, to string) *GraphEdge {
	for i := range g.Edges {
		if g.Edges[i].From == from && g.Edges[i].To == to {
			return &g.Edges[i]
		}
	}
	return nil
}
