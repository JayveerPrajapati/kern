package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestUnifiedGraphDeduplicatesDeclarations(t *testing.T) {
	definition := index.Symbol{Name: "Run", Kind: "func", Lang: "go", File: "source.go", Line: 1}
	graph := FromIndex(&index.Index{Symbols: []index.Symbol{definition, definition}})
	count := 0
	for _, node := range graph.Nodes {
		if node.Kind == "symbol" {
			count++
			if node.Symbol.ID != definition.DeclarationID() {
				t.Fatal("context symbol lost declaration identity")
			}
		}
	}
	if count != 1 || len(graph.Edges) != 2 || graph.Version.SymbolCount != 1 {
		t.Fatalf("duplicate declaration nodes/containment survived: %+v", graph.Graph)
	}
}

func TestUnifiedGraphDeclarationIdentityAndSourceOwnership(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "run", Kind: "func", File: "a.go", Line: 1, Lang: "python"},
			{Name: "run", Kind: "func", File: "b.go", Line: 1, Lang: "python"},
			{Name: "helper", Kind: "func", File: "a.go", Line: 4, Lang: "python"},
			{Name: "helper", Kind: "func", File: "b.go", Line: 4, Lang: "python"},
		},
		Calls: map[string][]index.CallEdge{"run": {
			{Target: "helper", SourceFile: "a.go", Confidence: index.ConfidenceHigh},
			{Target: "helper", SourceFile: "b.go", Confidence: index.ConfidenceMedium},
		}},
	}
	graph := FromIndex(ix)
	for _, definition := range ix.Symbols {
		if _, found := graph.NodeByID(definition.DeclarationID()); !found {
			t.Fatalf("unified graph discarded declaration %s", definition.DeclarationID())
		}
	}
	if _, resolved := graph.ResolveNodeID("run"); resolved {
		t.Fatal("ambiguous legacy label resolved to an arbitrary declaration")
	}
	for position, definition := range ix.Symbols[:2] {
		dependencies := graph.WhatDoesXDependOn(definition.DeclarationID())
		if len(dependencies) != 1 || dependencies[0].ID != ix.Symbols[position+2].DeclarationID() {
			t.Fatalf("source ownership leaked: %+v", dependencies)
		}
	}
}

func TestUnifiedGraphReferenceIsOpaque(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Name: "Use", Kind: "func", File: "App.java", Line: 1, Lang: "java"},
			{Name: "Fetch", Receiver: "App", Kind: "method", File: "App.java", Line: 2, Lang: "java"},
			{Name: "Fetch", Receiver: "App", Kind: "method", File: "App.java", Line: 4, Lang: "java"},
			{Name: "Danger", Kind: "func", File: "App.java", Line: 8, Lang: "java"},
		},
		Calls: map[string][]index.CallEdge{
			"Use":       {{Target: "App.Fetch", SourceFile: "App.java", Confidence: index.ConfidenceHigh}},
			"App.Fetch": {{Target: "Danger", SourceFile: "App.java", Confidence: index.ConfidenceHigh}},
		},
	}
	graph := FromIndex(ix)
	dependencies := graph.WhatDoesXDependOn("Use")
	if len(dependencies) != 1 || dependencies[0].Kind != "reference" {
		t.Fatalf("overload ambiguity silently expanded or disappeared: %+v", dependencies)
	}
	if dependencies[0].Label != "App.Fetch" {
		t.Fatalf("reference label lost: %+v", dependencies[0])
	}
}
