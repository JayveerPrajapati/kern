package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGoCompilerBindingsPromotedAliasesAndGenerics(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":     "module demo\n\ngo 1.22\n",
		"a/base.go":  "package a\ntype Base struct{}\nfunc (Base) Hit() {}\nfunc Identity[T any](value T) T { return value }\n",
		"a/outer.go": "package a\ntype Outer struct { Base }\ntype Alias = Outer\nfunc Local(value Alias) { value.Hit(); Identity[int](1) }\n",
		"b/base.go":  "package b\ntype Base struct{}\nfunc (Base) Hit() {}\n",
		"c/use.go":   "package c\nimport \"demo/a\"\nfunc Use(value a.Alias) { value.Hit(); a.Identity[int](2) }\nfunc Dynamic(fn func()) { fn() }\n",
	})
	ix, err := BuildWithOptions(root, WithGoTypes())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, symbol := range ix.Symbols {
		ids[symbol.File+":"+symbol.FullName()] = symbol.DeclarationID()
	}
	for _, owner := range []string{"Local", "Use"} {
		seen := map[string]bool{}
		for _, edge := range ix.Calls[owner] {
			if edge.Binding == "go/types" && edge.FromID != "" {
				seen[edge.ToID] = true
			}
		}
		if !seen[ids["a/base.go:Base.Hit"]] || !seen[ids["a/base.go:Identity"]] || seen[ids["b/base.go:Base.Hit"]] {
			t.Fatalf("%s compiler bindings = %v; calls = %+v", owner, seen, ix.Calls[owner])
		}
	}
	for _, edge := range ix.Calls["Dynamic"] {
		if edge.Binding != "go/types" || !edge.Dynamic || edge.ToID != "" {
			t.Fatalf("function variable was falsely statically bound: %+v", edge)
		}
	}
	graph := ix.CallBindings()
	for _, edge := range graph.Edges {
		if edge.From == ids["c/use.go:Use"] && edge.To == ids["b/base.go:Base.Hit"] {
			t.Fatalf("compiler selection replaced with a homonym: %+v", edge)
		}
	}
	data, err := json.Marshal(ix)
	if err != nil {
		t.Fatal(err)
	}
	var restored Index
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !restored.GoTypes || restored.Calls["Use"][0].Binding != "go/types" {
		t.Fatal("JSON dropped compiler binding metadata")
	}
	if err := os.WriteFile(filepath.Join(root, "a/base.go"), []byte("package a\n\ntype Base struct{}\nfunc (Base) Hit() {}\nfunc Identity[T any](value T) T { return value }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(root, ix)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.GoTypes {
		t.Fatal("incremental update lost compiler mode")
	}
	for _, symbol := range updated.Symbols {
		if symbol.File == "a/base.go" && symbol.FullName() == "Base.Hit" {
			found := false
			for _, edge := range updated.Calls["Use"] {
				found = found || edge.ToID == symbol.DeclarationID()
			}
			if !found || symbol.DeclarationID() == ids["a/base.go:Base.Hit"] {
				t.Fatal("unchanged caller kept stale compiler declaration ID")
			}
		}
	}
}

func TestGoCompilerBindingsSkipBrokenPackages(t *testing.T) {
	ix, err := BuildWithOptions(writeTree(t, map[string]string{
		"go.mod":     "module demo\n\ngo 1.22\n",
		"ok/ok.go":   "package ok\nfunc One() { Two() }\nfunc Two() {}\n",
		"bad/bad.go": "package bad\nfunc Broken() { missing() }\n",
	}), WithGoTypes())
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.Calls["One"]) != 1 || ix.Calls["One"][0].Binding != "go/types" || ix.Calls["One"][0].ToID == "" {
		t.Fatalf("valid package was not compiler bound: %+v", ix.Calls["One"])
	}
	if len(ix.Calls["Broken"]) != 1 || ix.Calls["Broken"][0].Binding != "" {
		t.Fatalf("broken package received compiler claims: %+v", ix.Calls["Broken"])
	}
}

func TestGoCompilerBindingsKeepInitOwnersSeparate(t *testing.T) {
	ix, err := BuildWithOptions(writeTree(t, map[string]string{
		"go.mod":  "module demo\n\ngo 1.22\n",
		"main.go": "package demo\nfunc One() {}\nfunc Two() {}\nfunc init() { One() }\nfunc init() { Two() }\ntype Service interface { One() }\nfunc Interface(value Service) { value.One() }\nfunc factory() func() { return One }\nfunc Callback() { factory()() }\n",
	}), WithGoTypes())
	if err != nil {
		t.Fatal(err)
	}
	owners := map[string]bool{}
	for _, edge := range ix.Calls["init"] {
		if edge.Binding != "go/types" || edge.FromID == "" || edge.ToID == "" {
			t.Fatalf("init call not compiler bound: %+v", edge)
		}
		owners[edge.FromID] = true
	}
	if len(owners) != 2 || len(ix.Calls["init"]) != 2 {
		t.Fatalf("same-file init owners were pooled: %+v", ix.Calls["init"])
	}
	if len(ix.Calls["Interface"]) != 1 || ix.Calls["Interface"][0].ToID == "" {
		t.Fatalf("interface static declaration was not bound: %+v", ix.Calls["Interface"])
	}
	unknown := 0
	for _, edge := range ix.Calls["Callback"] {
		if edge.Dynamic && edge.Binding == "go/types" && edge.ToID == "" {
			unknown++
		}
	}
	if unknown != 1 {
		t.Fatalf("returned callback was falsely bound as a direct call: %+v", ix.Calls["Callback"])
	}
}

func TestGoCompilerBindingsClearClaimsOnBrokenUpdate(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":  "module demo\n\ngo 1.22\n",
		"main.go": "package demo\nfunc Use() { Work() }\nfunc Work() {}\n",
	})
	ix, err := BuildWithOptions(root, WithGoTypes())
	if err != nil {
		t.Fatal(err)
	}
	if ix.Calls["Use"][0].Binding != "go/types" {
		t.Fatal("fixture lacks compiler bindings")
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package demo\nfunc Broken() { missing() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(root, ix)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range updated.Calls["Use"] {
		if edge.Binding != "" || edge.FromID != "" || edge.ToID != "" {
			t.Fatalf("broken package retained obsolete compiler claims: %+v", edge)
		}
	}
}

func TestCompilerBindingsDoNotFallBackFromStaleIDs(t *testing.T) {
	owner := Symbol{Kind: "func", Name: "Use", File: "a.go", Line: 1}
	callee := Symbol{Kind: "func", Name: "Work", File: "a.go", Line: 2}
	ix := &Index{
		Symbols: []Symbol{owner, callee},
		Calls:   map[string][]CallEdge{"Use": {{Target: "Work", SourceFile: "a.go", Binding: "go/types", FromID: owner.DeclarationID(), ToID: "decl:stale", Confidence: ConfidenceHigh}}},
	}
	graph := ix.CallBindings()
	if len(graph.Edges) != 1 || graph.Edges[0].Resolution == "resolved" || graph.Edges[0].To == callee.DeclarationID() {
		t.Fatalf("stale compiler target replaced by a name guess: %+v", graph)
	}
}
