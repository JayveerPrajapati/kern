package index

import (
	"strings"
	"testing"
)

// TestGraphHTMLStaleBannerFresh: a freshly built index (t.TempDir, non-git)
// must render GraphHTML and Mermaid without any staleness banner — FileHashes
// still match the on-disk content.
func TestGraphHTMLStaleBannerFresh(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.FileHashes) == 0 {
		t.Fatal("Build must populate FileHashes for the staleness check")
	}
	ng, ok := ix.Neighborhood("greet")
	if !ok {
		t.Fatal("no neighborhood for greet")
	}
	html := ng.GraphHTML(ix)
	if strings.Contains(html, "changed since index") {
		t.Error("fresh index rendered a stale banner in GraphHTML")
	}
	if strings.Contains(html, `id="stale"`) {
		t.Error("fresh index rendered the #stale div in GraphHTML")
	}
	if m := ix.Mermaid("greet"); strings.Contains(m, "%% ") {
		t.Errorf("fresh index rendered a stale banner in Mermaid:\n%s", m)
	}
}

// TestGraphHTMLStaleBannerStale: mutating a cited file's recorded hash (no
// disk rewrite) must surface the banner in neighborhood GraphHTML, whole-graph
// GraphHTML, and the Mermaid output.
func TestGraphHTMLStaleBannerStale(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic staleness: flip the recorded hash of a file the graph
	// cites, without touching the file on disk.
	ix.FileHashes["main.go"] = "deadbeefdeadbeefdeadbeefdeadbeef"
	if !ix.fileStale("main.go") {
		t.Fatal("mutated FileHashes must report main.go as stale")
	}

	ng, ok := ix.Neighborhood("greet")
	if !ok {
		t.Fatal("no neighborhood for greet")
	}
	html := ng.GraphHTML(ix)
	if !strings.Contains(html, "changed since index") {
		t.Error("stale index: GraphHTML missing 'changed since index' banner text")
	}
	if !strings.Contains(html, `id="stale"`) {
		t.Error("stale index: GraphHTML missing the #stale div")
	}

	m := ix.Mermaid("greet")
	if !strings.Contains(m, "%% ") {
		t.Errorf("stale index: Mermaid missing '%% ' banner line:\n%s", m)
	}

	wg := ix.WholeGraph(0)
	wh := wg.GraphHTML(ix)
	if !strings.Contains(wh, "changed since index") {
		t.Error("stale index: whole-graph GraphHTML missing 'changed since index' banner text")
	}
	if !strings.Contains(wh, `id="stale"`) {
		t.Error("stale index: whole-graph GraphHTML missing the #stale div")
	}
}