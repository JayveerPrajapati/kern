package index

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestForeignNamedAndNamespaceBindings(t *testing.T) {
	for _, fixture := range []struct {
		language, extension, library, source string
	}{
		{"python", ".py", "def public():\n    pass\n", "from lib.core import public as invoke\nimport lib.core as ns\ndef use():\n    invoke()\n    ns.public()\n"},
		{"javascript", ".js", "export function publicCall() {}\n", "import { publicCall as invoke } from '../lib/core';\nimport * as ns from '../lib/core';\nexport function use() { invoke(); ns.publicCall(); }\n"},
		{"typescript", ".ts", "export function publicCall() {}\n", "import { publicCall as invoke } from '../lib/core';\nimport * as ns from '../lib/core';\nexport function use() { invoke(); ns.publicCall(); }\n"},
	} {
		t.Run(fixture.language, func(t *testing.T) {
			ix, err := Build(writeTree(t, map[string]string{
				"lib/core" + fixture.extension:       fixture.library,
				"app/use" + fixture.extension:        fixture.source,
				"unrelated/core" + fixture.extension: fixture.library,
			}))
			if err != nil {
				t.Fatal(err)
			}
			graph, found := ix.Neighborhood("use")
			if !found || len(graph.Edges) != 2 || graph.UnresolvedCalls != 0 {
				t.Fatalf("import aliases did not bind: %+v", graph)
			}
			nodes := map[string]GraphNode{}
			for _, node := range graph.Nodes {
				nodes[node.ID] = node
			}
			for _, edge := range graph.Edges {
				if nodes[edge.To].File != "lib/core"+fixture.extension {
					t.Fatalf("alias bound to unrelated module: %+v", edge)
				}
			}
		})
	}
}

func aliasPersistenceFixture(t *testing.T) *Index {
	ix, err := Build(writeTree(t, map[string]string{
		"lib/core.go":  "package lib\nfunc Public() {}\n",
		"app/use.go":   "package app\nimport remote \"demo/lib\"\nfunc Use() { remote.Public(); { var remote interface{ Public() }; remote.Public() } }\n",
		"lib/core.py":  "def public():\n    pass\n",
		"app/use.py":   "from lib.core import public as invoke\ndef use():\n    invoke()\n",
		"lib/dflt.ts":  "export default function defaultCall() {}\nexport * from './core2';\n",
		"lib/core2.ts": "export function starred() {}\n",
		"app/use.ts":   "import Dflt from '../lib/dflt';\nimport { starred } from '../lib/dflt';\nexport function useTs() { Dflt(); starred(); }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return ix
}
func assertBindingPersistence(t *testing.T, original, loaded *Index) {
	t.Helper()
	if !reflect.DeepEqual(original.Calls, loaded.Calls) || !reflect.DeepEqual(original.ImportsByFile, loaded.ImportsByFile) {
		t.Fatal("persistence changed call or import binding metadata")
	}
	if len(original.ExportsByFile) == 0 || !reflect.DeepEqual(original.ExportsByFile, loaded.ExportsByFile) {
		t.Fatalf("persistence changed export metadata: %v vs %v", original.ExportsByFile, loaded.ExportsByFile)
	}
	if !reflect.DeepEqual(original.CallBindings(), loaded.CallBindings()) {
		t.Fatal("persistence changed declaration bindings")
	}
}

func TestJSONAliasAndDynamicRoundTrip(t *testing.T) {
	ix := aliasPersistenceFixture(t)
	writeJSONFixture(t, ix)
	loaded, err := LoadFile(StorePath(ix.Root))
	if err != nil {
		t.Fatal(err)
	}
	assertBindingPersistence(t, ix, loaded)
}

func TestBindingCacheConcurrentAndUpdate(t *testing.T) {
	ix := aliasPersistenceFixture(t)
	want := aliasPersistenceFixture(t).CallBindings()
	var workers sync.WaitGroup
	for count := 0; count < 8; count++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if !reflect.DeepEqual(ix.CallBindings(), want) {
				t.Error("concurrent bindings changed")
			}
		}()
	}
	workers.Wait()
	if err := os.WriteFile(filepath.Join(ix.Root, "app/use.go"), []byte("package app\nimport remote \"demo/lib\"\nfunc Use() { remote.Missing() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := Update(ix.Root, ix)
	if err != nil {
		t.Fatal(err)
	}
	graph, found := updated.Neighborhood("Use")
	if !found || len(graph.Edges) != 1 || graph.UnresolvedCalls != 1 {
		t.Fatalf("update reused stale bindings: %+v", graph)
	}
	if !reflect.DeepEqual(ix.CallBindings(), want) {
		t.Fatal("update mutated previous binding snapshot")
	}
}

func TestDispatchPreservesSourceOwnedCallerIdentity(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Name: "Use", Kind: "func", Lang: "go", File: "a/source.go", Line: 1},
			{Name: "Use", Kind: "func", Lang: "go", File: "b/source.go", Line: 1},
			{Name: "Run", Receiver: "Service", Kind: "method", Lang: "go", File: "a/source.go", Line: 2},
		},
		Calls: map[string][]CallEdge{"Use": {
			{Target: "Interface.Run", SourceFile: "a/source.go", Confidence: ConfidenceMedium},
			{Target: "Interface.Run", SourceFile: "b/source.go", Confidence: ConfidenceMedium, Dynamic: true},
		}},
		Callers:     map[string][]string{},
		InheritedBy: map[string][]string{"Interface": {"Service"}},
	}
	ix.addDispatchEdges()
	graph := ix.CallBindings()
	count := 0
	for _, edge := range graph.Edges {
		if edge.Synth != "dispatch" {
			continue
		}
		count++
		if edge.SourceFile != "a/source.go" || edge.From != ix.Symbols[0].DeclarationID() || edge.To != ix.Symbols[2].DeclarationID() || edge.Confidence != "low" {
			t.Fatalf("dispatch lost source identity or expanded dynamic receiver: %+v", edge)
		}
	}
	if count != 1 {
		t.Fatalf("dispatch edges = %d, want 1", count)
	}
}

func TestGoAliasAndQualifiedReceiverBindings(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"go.mod":       "module demo\n\ngo 1.23\n",
		"lib/lib.go":   "package library\ntype Client struct {}\nfunc (Client) Read() {}\nfunc Public() {}\n",
		"app/app.go":   "package app\nimport remote \"demo/lib\"\ntype Client struct {}\nfunc (Client) Read() {}\nfunc Use(client *remote.Client) { remote.Public(); client.Read(); value := remote.Client{}; value.Read() }\n",
		"app/other.go": "package app\nimport alternate \"demo/lib\"\nfunc Other() { alternate.Public(); remote.Public(); library.Public() }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	graph, found := ix.Neighborhood("Use")
	if !found || len(graph.Edges) != 2 {
		t.Fatalf("aliased namespace/type calls lost: %+v", graph)
	}
	nodes := map[string]GraphNode{}
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
	}
	for _, edge := range graph.Edges {
		if edge.Resolution != "resolved" || nodes[edge.To].File != "lib/lib.go" {
			t.Fatalf("imported receiver bound to local homonym: %+v", edge)
		}
	}
	other, _ := ix.Neighborhood("Other")
	resolved := 0
	for _, edge := range other.Edges {
		if edge.Resolution == "resolved" {
			resolved++
		}
	}
	if resolved != 1 || other.UnresolvedCalls != 2 {
		t.Fatalf("aliases leaked across files or default namespace captured an alias: %+v", other)
	}
}

func TestGoTypedReceiverLexicalScope(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"app.go": "package app\ntype First struct {}\ntype Second struct {}\nfunc (First) Read() {}\nfunc (Second) Read() {}\nfunc Use(value First) { value.Read(); { value := Second{}; value.Read() }; value.Read() }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	graph, _ := ix.Neighborhood("Use")
	if len(graph.Edges) != 2 {
		t.Fatalf("nested type shadowing overwrote enclosing receiver: %+v", graph)
	}
	for _, edge := range graph.Edges {
		if edge.Resolution != "resolved" {
			t.Fatalf("explicit receiver type lost: %+v", edge)
		}
	}
}

func TestGoAliasShadowedByUnknownLocal(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"lib/lib.go": "package lib\nfunc Public() {}\n",
		"app/app.go": "package app\nimport remote \"demo/lib\"\nfunc Use() { remote.Public(); { var remote interface{ Public() }; remote.Public() }; remote.Public() }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	graph, _ := ix.Neighborhood("Use")
	if len(graph.Edges) != 2 || graph.UnresolvedCalls != 1 {
		t.Fatalf("unknown local shadow falsely captured by imported namespace: %+v", graph)
	}
}

func TestGoDefaultDotAndBlankImportBindings(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"lib/lib.go":     "package library\nfunc Public() {}\n",
		"default/app.go": "package app\nimport \"demo/lib\"\nfunc Use() { library.Public(); lib.Public() }\n",
		"dot/app.go":     "package app\nimport . \"demo/lib\"\nfunc Dot() { Public() }\n",
		"blank/app.go":   "package app\nimport _ \"demo/lib\"\nfunc Blank() { library.Public(); Public() }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		name                 string
		resolved, unresolved int
	}{{"Use", 1, 1}, {"Dot", 1, 0}, {"Blank", 0, 2}} {
		graph, _ := ix.Neighborhood(query.name)
		if len(graph.Edges)-graph.UnresolvedCalls != query.resolved || graph.UnresolvedCalls != query.unresolved {
			t.Fatalf("%s import semantics incorrect: %+v", query.name, graph)
		}
	}
}
