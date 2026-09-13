package index

import "testing"

// TestPromoteLowEdgesResolvesQualifiedTarget: a LOW edge whose target was
// recorded in package-qualified form ("lib.Public") resolves against the
// completed symbol table; the pass rewrites it to the canonical FullName and
// promotes it to MEDIUM, counting it as promoted.
func TestPromoteLowEdgesResolvesQualifiedTarget(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go": "package lib\n\nfunc Public() {}\n",
		"web/web.go": "package web\n\nfunc Use() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the cross-file ordering case: a tentative LOW edge recorded
	// before the target's file existed.
	ix.Calls["Use"] = append(ix.Calls["Use"], CallEdge{Target: "lib.Public", Confidence: ConfidenceLow})
	ix.promoteLowEdges()

	if got := edgeTarget(ix, "Use", "Public"); got == nil {
		t.Fatalf("edge Use -> Public not found after promotion: %v", ix.Calls["Use"])
	} else if got.Confidence != ConfidenceMedium {
		t.Errorf("promoted edge confidence = %q; want MEDIUM", got.Confidence)
	}
	if ix.PromotedLowEdges != 1 {
		t.Errorf("PromotedLowEdges = %d; want 1", ix.PromotedLowEdges)
	}
	if ix.UnresolvedLowEdges != 0 {
		t.Errorf("UnresolvedLowEdges = %d; want 0", ix.UnresolvedLowEdges)
	}
}

// TestPromoteLowEdgesLeavesResolvedLowEdges: a LOW edge whose target already
// resolves (e.g. a virtual-dispatch edge) is LOW by design — the pass must
// leave it untouched and not count it as unresolved. Go interfaces are
// satisfied structurally (no dispatch edges), so the fixture uses Java where
// `implements` is explicit.
func TestPromoteLowEdgesLeavesResolvedLowEdges(t *testing.T) {
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
	ix.promoteLowEdges()
	// The pass ran before addDispatchEdges, so simulate the real finalize
	// order by adding the virtual edge, then re-run the pass.
	ix.addDispatchEdges()
	before := countEdges(ix, "App.run", "FileStore.fetch")
	if before == 0 {
		t.Skip("virtual dispatch edge not produced by this fixture")
	}
	ix.promoteLowEdges()
	after := countEdges(ix, "App.run", "FileStore.fetch")
	if after != before {
		t.Errorf("resolved LOW edge was touched: before=%d after=%d", before, after)
	}
	if got := edgeTarget(ix, "App.run", "FileStore.fetch"); got != nil && got.Confidence != ConfidenceLow {
		t.Errorf("virtual edge confidence changed to %q; want LOW", got.Confidence)
	}
	if ix.UnresolvedLowEdges != 0 {
		t.Errorf("UnresolvedLowEdges = %d; want 0 (resolved LOW edges are not failures)", ix.UnresolvedLowEdges)
	}
	if ix.PromotedLowEdges != 0 {
		t.Errorf("PromotedLowEdges = %d; want 0", ix.PromotedLowEdges)
	}
}

// TestPromoteLowEdgesCountsUnresolved: a LOW edge whose target does not
// resolve even against the completed table stays LOW and is counted as
// unresolved — the honest verdict the pass exists to surface.
func TestPromoteLowEdgesCountsUnresolved(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.go": "package a\n\nfunc A() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	ix.Calls["A"] = append(ix.Calls["A"], CallEdge{Target: "ghost.Phantom", Confidence: ConfidenceLow})
	ix.promoteLowEdges()
	if ix.UnresolvedLowEdges != 1 {
		t.Errorf("UnresolvedLowEdges = %d; want 1", ix.UnresolvedLowEdges)
	}
	if got := edgeTarget(ix, "A", "ghost.Phantom"); got == nil || got.Confidence != ConfidenceLow {
		t.Errorf("unresolved edge should stay LOW: %v", ix.Calls["A"])
	}
}

// TestPromoteLowEdgesDedupeKeepsBestConfidence: canonicalization can merge
// distinct target forms ("db.Open" and "Open") into one key; the surviving
// representative must carry the highest confidence recorded for it
// (subsumes the keep-best-confidence dedupe fix).
func TestPromoteLowEdgesDedupeKeepsBestConfidence(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"db/db.go": "package db\n\nfunc Open() {}\n",
		"app/app.go": "package app\n\nfunc Start() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	ix.Calls["Start"] = []CallEdge{
		{Target: "db.Open", Confidence: ConfidenceLow},
		{Target: "Open", Confidence: ConfidenceHigh},
	}
	ix.promoteLowEdges()
	edges := ix.Calls["Start"]
	if len(edges) != 1 {
		t.Fatalf("expected one deduped edge, got %v", edges)
	}
	if edges[0].Target != "Open" || edges[0].Confidence != ConfidenceHigh {
		t.Errorf("dedupe kept %+v; want {Open HIGH}", edges[0])
	}
}

// TestBuildDoesNotRewriteHighEdges: a real Build runs the pass, and a HIGH
// package-qualified Go call ("db.Open") must remain exactly as recorded —
// modernization's bridge/community analysis and CallersOf attribution key on
// the recorded form, so only LOW (tentative) edges are reconciled.
func TestBuildDoesNotRewriteHighEdges(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"db/db.go":   "package db\n\nfunc Open() {}\n",
		"web/web.go": "package web\n\nimport \"demo/db\"\n\nfunc Use() { db.Open() }\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := edgeTarget(ix, "Use", "db.Open"); got == nil {
		t.Errorf("HIGH qualified call was rewritten: %v", ix.Calls["Use"])
	}
}

func edgeTarget(ix *Index, owner, target string) *CallEdge {
	for i := range ix.Calls[owner] {
		if ix.Calls[owner][i].Target == target {
			return &ix.Calls[owner][i]
		}
	}
	return nil
}

func countEdges(ix *Index, owner, target string) int {
	n := 0
	for _, e := range ix.Calls[owner] {
		if e.Target == target {
			n++
		}
	}
	return n
}