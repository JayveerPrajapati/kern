package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestEdgeSynthLabelRouterHop: an http.HandleFunc registration synthesizes
// the dispatch hop; EdgeSynthLabel reports the provenance, EdgeConfidenceLabel
// still reports the MEDIUM tier (filters keep working).
func TestEdgeSynthLabelRouterHop(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"app.go": `package main

import "net/http"

func home(w http.ResponseWriter, r *http.Request) {}

func setup() {
	http.HandleFunc("/", home)
}
`,
	})
	ix := buildIndex(t, dir)
	if got := EdgeSynthLabel(ix, "setup", "home"); got != "router:net-http" {
		t.Errorf("EdgeSynthLabel(setup, home) = %q, want router:net-http", got)
	}
	if got := EdgeConfidenceLabel(ix, "setup", "home"); got != "INFERRED" {
		t.Errorf("EdgeConfidenceLabel(setup, home) = %q, want INFERRED (MEDIUM tier)", got)
	}
	// The synthetic hop is reachable from the handler side too (path/why
	// traverse edges in both directions).
	if got := EdgeSynthLabel(ix, "home", "setup"); got != "router:net-http" {
		t.Errorf("EdgeSynthLabel(home, setup) = %q, want router:net-http", got)
	}
}

// TestExploreRenderAnnotatesSynthesized: explore output marks the hop
// (SYNTHESIZED: router:net-http) next to its [INFERRED] tier.
func TestExploreRenderAnnotatesSynthesized(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"app.go": `package main

import "net/http"

func home(w http.ResponseWriter, r *http.Request) {}

func setup() {
	http.HandleFunc("/", home)
}
`,
	})
	ix := buildIndex(t, dir)
	rep, err := Explore(ix, "home", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderExplore(rep)
	if !strings.Contains(out, "(SYNTHESIZED: router:net-http)") {
		t.Errorf("explore output missing SYNTHESIZED annotation:\n%s", out)
	}
	if !strings.Contains(out, "setup [INFERRED] (SYNTHESIZED: router:net-http)") {
		t.Errorf("caller row missing tier+synth combo:\n%s", out)
	}
}

// TestPathRenderAnnotatesSynthesized: path hops carry the marker.
func TestPathRenderAnnotatesSynthesized(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"app.go": `package main

import "net/http"

func home(w http.ResponseWriter, r *http.Request) {}

func setup() {
	http.HandleFunc("/", home)
}

func main() {
	setup()
}
`,
	})
	ix := buildIndex(t, dir)
	p := ShortestPath(ix, "main", "home")
	if len(p) == 0 {
		t.Fatal("no path main -> home")
	}
	rendered := RenderPath(ix, p)
	if !strings.Contains(rendered, "SYNTHESIZED: router:net-http") {
		t.Errorf("path render missing SYNTHESIZED marker:\n%s", rendered)
	}
}

// TestEdgeSynthLabelEmptyForSyntacticCalls: ordinary syntactic calls carry
// no synthesis provenance.
func TestEdgeSynthLabelEmptyForSyntacticCalls(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"app.go": `package main

func helper() int { return 1 }

func main() {
	_ = helper()
}
`,
	})
	ix := buildIndex(t, dir)
	if got := EdgeSynthLabel(ix, "main", "helper"); got != "" {
		t.Errorf("EdgeSynthLabel(main, helper) = %q, want empty", got)
	}
	if got := EdgeConfidenceLabel(ix, "main", "helper"); got != "EXTRACTED" {
		t.Errorf("EdgeConfidenceLabel(main, helper) = %q, want EXTRACTED", got)
	}
	_ = index.Index{}
}
