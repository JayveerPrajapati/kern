package topology

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func declarationTestLabels(ix *index.Index, names map[string]string) map[string]string {
	labels := map[string]string{}
	for _, symbol := range ix.Symbols {
		if label, found := names[symbol.FullName()]; found {
			labels[symbol.DeclarationID()] = label
		}
	}
	return labels
}

func TestSurprisingConnectionsDoNotPoolHomonymBridges(t *testing.T) {
	ix := buildIndex(t, writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": "package a\nimport \"demo/b\"\nfunc Start() { b.Work() }\n",
		"b/b.go": "package b\nfunc Work() {}\n",
		"c/c.go": "package c\nfunc Work() {}\n",
		"d/d.go": "package d\nimport \"demo/c\"\nfunc Other() { c.Work() }\n",
	}))
	labels := map[string]string{}
	for _, symbol := range ix.Symbols {
		labels[symbol.DeclarationID()] = symbol.File
	}
	edges := rankSurprisingConnections(ix, labels, 10)
	if len(edges) != 2 {
		t.Fatalf("homonyms suppressed as a pooled bridge: %+v", edges)
	}
	for _, edge := range edges {
		if edge.Edges != 1 || edge.Line != 2 {
			t.Fatalf("pooled evidence or rarity: %+v", edge)
		}
		switch edge.Caller {
		case "Start":
			if edge.Callee != "b.Work" || edge.File != "b/b.go" {
				t.Fatalf("wrong declaration evidence: %+v", edge)
			}
		case "Other":
			if edge.Callee != "c.Work" || edge.File != "c/c.go" {
				t.Fatalf("wrong declaration evidence: %+v", edge)
			}
		default:
			t.Fatalf("unexpected caller: %+v", edge)
		}
	}
}

func TestArchitectureCouplingEndpointsMatchReports(t *testing.T) {
	ix := &index.Index{Calls: map[string][]index.CallEdge{}, ImportsByFile: map[string][]index.ImportEdge{
		"a/a.go": {{Path: "demo/b", Confidence: index.ConfidenceHigh}},
	}}
	boundary := map[string]index.Symbol{}
	for _, group := range []string{"a", "b"} {
		for position := 1; position <= 4; position++ {
			symbol := index.Symbol{Kind: "func", Name: fmt.Sprintf("%s%d", strings.ToUpper(group), position), File: group + "/" + group + ".go", Line: position * 10}
			ix.Symbols = append(ix.Symbols, symbol)
			if boundary[group].Name == "" || symbol.DeclarationID() > boundary[group].DeclarationID() {
				boundary[group] = symbol
			}
		}
	}
	for _, caller := range ix.Symbols {
		for _, callee := range ix.Symbols {
			if caller.File == callee.File && caller.Name != callee.Name {
				ix.Calls[caller.Name] = append(ix.Calls[caller.Name], index.CallEdge{Target: callee.Name, SourceFile: caller.File, Confidence: index.ConfidenceHigh})
			}
		}
	}
	from, to := boundary["a"], boundary["b"]
	coupling := index.CallEdge{Target: "b." + to.Name, SourceFile: from.File, Confidence: index.ConfidenceHigh}
	ix.Calls[from.Name] = append(ix.Calls[from.Name], coupling, coupling, index.CallEdge{Target: "missing", SourceFile: from.File, Confidence: index.ConfidenceHigh})
	architecture := AnalyzeArchitecture(ix)
	if len(architecture.Communities) != 2 || len(architecture.Coupling) != 1 {
		t.Fatalf("expected two clusters and one coupling: %+v", architecture)
	}
	ids := map[string]bool{}
	for _, community := range architecture.Communities {
		ids[community.ID] = true
	}
	edge := architecture.Coupling[0]
	if !ids[edge.From] || !ids[edge.To] || edge.Count != 1 || !reflect.DeepEqual(edge.Nodes, []string{from.Name + " -> " + to.Name}) {
		t.Fatalf("coupling endpoints or evidence mismatch: %+v", edge)
	}
	if !reflect.DeepEqual(architecture, AnalyzeArchitecture(ix)) {
		t.Fatal("architecture report is nondeterministic")
	}
}
