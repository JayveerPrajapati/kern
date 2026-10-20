package index

import (
	"reflect"
	"sync"
	"testing"
)

func TestInheritanceImportCacheIsImmutable(t *testing.T) {
	ix := New(t.TempDir())
	ix.Symbols = []Symbol{
		{Name: "Child", Kind: "struct", File: "app/child.go", Lang: "go", Line: 1},
		{Name: "Base", Kind: "struct", File: "lib/base.go", Lang: "go", Line: 1},
	}
	ix.ImportsByFile["app/child.go"] = []ImportEdge{
		{Path: "demo/lib", Alias: "remote"},
		{Path: "demo/missing", Alias: "absent"},
	}
	ix.Inherits["Child"] = []string{"embeds:remote.Base"}
	catalog := newExportCatalog(ix)
	wantDirectories := map[string]string{"demo/lib": "lib", "demo/missing": ""}
	if !reflect.DeepEqual(catalog.importDirs, wantDirectories) {
		t.Fatalf("binding cache was not fully initialized before publication: %+v", catalog.importDirs)
	}
	wantEdges, wantNodes := ix.InheritBindings()
	if len(wantEdges) != 1 || wantEdges[0].Resolution != "resolved" {
		t.Fatalf("imported base did not bind: %+v", wantEdges)
	}
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 10; iteration++ {
				edges, nodes := ix.InheritBindings()
				if !reflect.DeepEqual(edges, wantEdges) || !reflect.DeepEqual(nodes, wantNodes) {
					t.Error("concurrent inheritance bindings changed")
				}
			}
		}()
	}
	workers.Wait()
	if !reflect.DeepEqual(catalog.importDirs, wantDirectories) {
		t.Fatal("inheritance queries mutated the published binding cache")
	}
}

func TestInheritBindingsUseDeclarationIdentity(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"a/animal.ts": "export class Animal {}\n",
		"b/animal.ts": "export class Animal {}\n",
		"app/cat.ts":  "import { Animal } from '../a/animal';\nexport class Cat extends Animal {}\n",
		"app/dog.ts":  "export class Dog extends Missing {}\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	edges, nodes := ix.InheritBindings()
	byID := map[string]GraphNode{}
	for _, node := range nodes {
		byID[node.ID] = node
	}
	var cat, dog bool
	for _, edge := range edges {
		from, to := byID[edge.From], byID[edge.To]
		switch from.Name {
		case "Cat":
			cat = true
			if edge.Resolution != "resolved" || to.File != "a/animal.ts" {
				t.Errorf("Cat did not bind to the imported Animal: %+v -> %+v", edge, to)
			}
		case "Dog":
			dog = true
			if edge.Resolution != "unresolved" || len(to.Candidates) != 0 {
				t.Errorf("unknown base was guessed: %+v -> %+v", edge, to)
			}
		}
	}
	if !cat || !dog {
		t.Fatalf("missing inheritance edges: %+v", edges)
	}
}
