package topology

import (
	"math"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestHubsAndBridgesUseDeclarationDegrees(t *testing.T) {
	ix := buildIndex(t, writeTree(t, map[string]string{
		"go.mod":      "module demo\n\ngo 1.22\n",
		"a/a.go":      "package a\nfunc Load() { depA() }\nfunc depA() {}\n",
		"b/b.go":      "package b\nfunc Load() { depB() }\nfunc depB() {}\n",
		"c/c.go":      "package c\nimport \"demo/a\"\nfunc Use() { a.Load(); a.Load() }\n",
		"d/d.go":      "package d\nimport \"demo/a\"\nfunc Use() { a.Load() }\n",
		"e/e.go":      "package e\nimport \"demo/b\"\nfunc Use() { b.Load() }\n",
		"unused/u.go": "package unused\nfunc Load() {}\n",
	}))
	ix.Communities = map[string]string{"Load": "incorrect", "Use": "incorrect"}
	hubs := Hubs(ix, 100)
	byFile := map[string]Hub{}
	for _, hub := range hubs {
		if hub.Symbol == "a.Load" || hub.Symbol == "b.Load" {
			byFile[hub.File] = hub
		}
		if hub.Symbol == "unused.Load" || hub.Symbol == "Load" || hub.ID == "" {
			t.Fatalf("pooled or unidentified hub: %+v", hub)
		}
	}
	for file, want := range map[string]int{"a/a.go": 2, "b/b.go": 1} {
		hub, found := byFile[file]
		if !found || hub.Callers != want || hub.Calls != 1 || hub.Score != want*2+1 {
			t.Fatalf("pooled incoming/outgoing degree for %s: %+v", file, hub)
		}
		labels := ix.DeclarationCommunities()
		size := communitySizes(labels)[labels[hub.ID]]
		if math.Abs(hub.Weighted-float64(hub.Score)/math.Sqrt(float64(size))) > 1e-9 {
			t.Fatalf("normalization used name-keyed communities: %+v", hub)
		}
	}
	bridges := Bridges(ix, 10)
	if len(bridges) != 1 || bridges[0].Symbol != "a.Load" || bridges[0].Callers != 2 || len(bridges[0].Packages) != 2 || bridges[0].ID != byFile["a/a.go"].ID {
		t.Fatalf("pooled bridge caller identities: %+v", bridges)
	}
	for _, symbol := range ix.Symbols {
		if symbol.File == "unused/u.go" && HubSet(ix)[symbol.DeclarationID()] {
			t.Fatal("unused homonym inherited a hub flag")
		}
	}
}

func TestHubLegacyCallerFallbackRejectsHomonyms(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Load", File: "a/a.go", Line: 1},
			{Kind: "func", Name: "Load", File: "b/b.go", Line: 1},
			{Kind: "func", Name: "Use", File: "c/c.go", Line: 1},
		},
		Callers: map[string][]string{"Load": {"Use"}},
	}
	if hubs := Hubs(ix, 10); len(hubs) != 0 {
		t.Fatalf("unattributed legacy callers assigned to homonyms: %+v", hubs)
	}
}
