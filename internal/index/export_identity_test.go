package index

import (
	"encoding/json"
	"encoding/xml"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func exportIdentityFixture() *Index {
	return &Index{
		Root: ".",
		Symbols: []Symbol{
			{Name: "run", File: "a/main.py", Line: 1, Kind: "func", Lang: "python"},
			{Name: "run", File: "b/main.py", Line: 1, Kind: "func", Lang: "python"},
			{Name: "helper", File: "a/main.py", Line: 4, Kind: "func", Lang: "python"},
			{Name: "helper", File: "b/main.py", Line: 4, Kind: "func", Lang: "python"},
		},
		Calls: map[string][]CallEdge{
			"run": {
				{Target: "helper", SourceFile: "a/main.py", Confidence: ConfidenceHigh},
				{Target: "helper", SourceFile: "b/main.py", Confidence: ConfidenceMedium},
			},
		},
	}
}

func TestGraphExportPreservesDeclarations(t *testing.T) {
	ix := exportIdentityFixture()
	graph := ix.WholeGraph(10)
	if len(graph.Nodes) != 4 || len(graph.Edges) != 2 {
		t.Fatalf("declarations or calls collapsed: %+v", graph)
	}
	nodes := map[string]GraphNode{}
	for _, node := range graph.Nodes {
		nodes[node.ID] = node
	}
	for _, symbol := range ix.Symbols {
		if _, ok := nodes[symbol.DeclarationID()]; !ok {
			t.Fatalf("declaration missing: %s", symbol.DeclarationID())
		}
	}
	for _, edge := range graph.Edges {
		if nodes[edge.From].File != nodes[edge.To].File {
			t.Fatalf("cross-file leakage: %+v", edge)
		}
		want := confHigh
		if nodes[edge.From].File == "b/main.py" {
			want = confMedium
		}
		if edge.Confidence != want {
			t.Fatalf("pooled confidence: %+v", edge)
		}
	}
}

func TestGraphCypherMatchesIdentityNotName(t *testing.T) {
	graph := GraphResult{
		Nodes: []GraphNode{{ID: "decl:a", Name: "run"}, {ID: "decl:b", Name: "run"}},
		Edges: []GraphEdge{{From: "decl:a", To: "decl:b", Confidence: confHigh}},
	}
	out := graph.GraphCypher()
	if !strings.Contains(out, `MATCH (a:Symbol {id: "decl:a"}), (b:Symbol {id: "decl:b"})`) {
		t.Fatalf("Cypher matches labels instead of identity:\n%s", out)
	}
}

func TestGraphNeighborhoodRootIdentity(t *testing.T) {
	ix := exportIdentityFixture()
	graph, found := ix.Neighborhood("run")
	if !found || len(graph.RootIDs) != 2 || len(graph.Edges) != 2 {
		t.Fatalf("ambiguous query lost candidates: %+v", graph)
	}
	root := ix.Symbols[1]
	graph, found = ix.Neighborhood(root.DeclarationID())
	if !found || len(graph.RootIDs) != 1 || graph.RootIDs[0] != root.DeclarationID() || len(graph.Edges) != 1 {
		t.Fatalf("exact identity query not isolated: %+v", graph)
	}
	for _, node := range graph.Nodes {
		if node.File != "b/main.py" {
			t.Fatalf("other caller's declarations leaked: %+v", node)
		}
	}
}

func TestGraphExportAmbiguousOwnershipAndTargets(t *testing.T) {
	for _, missingSource := range []bool{false, true} {
		t.Run(map[bool]string{false: "overloads", true: "legacy"}[missingSource], func(t *testing.T) {
			ix := exportIdentityFixture()
			if !missingSource {
				ix.Symbols[1].File = ix.Symbols[0].File
				ix.Symbols[1].Line = 10
				ix.Symbols[3].File = ix.Symbols[2].File
				ix.Symbols[3].Line = 12
			}
			source := ix.Symbols[0].File
			if missingSource {
				source = ""
			}
			ix.Calls["run"] = []CallEdge{{Target: "helper", SourceFile: source, Confidence: ConfidenceHigh}}
			graph, found := ix.Neighborhood(ix.Symbols[0].DeclarationID())
			if !found || len(graph.Edges) != 1 || graph.Edges[0].Resolution != "ambiguous" {
				t.Fatalf("ambiguous call fabricated or dropped: %+v", graph)
			}
			for _, node := range graph.Nodes {
				if node.ID == graph.Edges[0].From || node.ID == graph.Edges[0].To {
					if node.Resolution != "ambiguous" || len(node.Candidates) != 2 || node.File != "" || node.Line != 0 {
						t.Fatalf("ambiguous reference acquired arbitrary location: %+v", node)
					}
				}
			}
			whole := ix.WholeGraph(10)
			if len(whole.Nodes) != 4 || len(whole.Edges) != 0 || whole.UnresolvedCalls != 1 {
				t.Fatalf("whole graph fabricated ambiguous edges: %+v", whole)
			}
		})
	}
}

func TestGraphExportLanguageIsolation(t *testing.T) {
	var symbols []Symbol
	calls := map[string][]CallEdge{}
	for position, language := range []string{"go", "python", "typescript", "java", "ruby", "rust", "c", "cpp", "csharp", "javascript", "shell", "php"} {
		file := "shared/main." + language
		receiver := ""
		if language == "java" || language == "csharp" {
			receiver = "App"
		}
		owner := Symbol{Name: "run", Receiver: receiver, Lang: language, File: file, Line: position + 1, Kind: "func"}
		symbols = append(symbols, owner, Symbol{Name: "helper", Receiver: receiver, Lang: language, File: file, Line: position + 20, Kind: "func"})
		calls[owner.FullName()] = append(calls[owner.FullName()], CallEdge{Target: "helper", SourceFile: file, Confidence: ConfidenceHigh})
	}
	ix := &Index{Symbols: symbols, Calls: calls}
	graph := ix.WholeGraph(100)
	if len(graph.Nodes) != len(symbols) || len(graph.Edges) != len(symbols)/2 {
		t.Fatalf("language-independent declarations collapsed: %+v", graph)
	}
	byID := map[string]Symbol{}
	for _, symbol := range symbols {
		byID[symbol.DeclarationID()] = symbol
	}
	for _, edge := range graph.Edges {
		if byID[edge.From].Lang != byID[edge.To].Lang {
			t.Fatalf("cross-language call fabricated: %+v", edge)
		}
	}
}

func TestGraphExportQualifiedImportBinding(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{
			{Name: "Use", File: "app/a.go", Lang: "go", Kind: "func"},
			{Name: "Use", File: "app/b.go", Lang: "go", Kind: "func"},
			{Name: "Public", File: "lib/lib.go", Lang: "go", Kind: "func"},
			{Name: "Public", File: "first/lib/lib.go", Lang: "go", Kind: "func"},
			{Name: "Public", File: "second/lib/lib.go", Lang: "go", Kind: "func"},
			{Name: "Println", File: "app/local.go", Lang: "go", Kind: "func"},
			{Name: "len", Receiver: "List", File: "app/local.go", Lang: "go", Kind: "method"},
			{Name: "len", File: "lib/lib.go", Lang: "go", Kind: "func"},
		},
		ImportsByFile: map[string][]ImportEdge{"app/a.go": {{Path: "demo/first/lib"}, {Path: "fmt"}}},
		Calls: map[string][]CallEdge{
			"Use": {
				{Target: "lib.Public", SourceFile: "app/a.go", Confidence: ConfidenceHigh},
				{Target: "lib.Public", SourceFile: "app/b.go", Confidence: ConfidenceHigh},
				{Target: "alias.Public", SourceFile: "app/a.go", Confidence: ConfidenceHigh},
				{Target: "fmt.Println", SourceFile: "app/a.go", Confidence: ConfidenceHigh},
				{Target: "len", SourceFile: "app/a.go", Confidence: ConfidenceHigh},
			},
		},
	}
	graph := ix.WholeGraph(20)
	if len(graph.Edges) != 1 || graph.UnresolvedCalls != 4 || graph.Edges[0].To != ix.Symbols[3].DeclarationID() {
		t.Fatalf("per-file imports, qualifier or builtin scoping violated: %+v", graph)
	}
}

func TestGraphExportDeterministicAndCapClosed(t *testing.T) {
	ix := exportIdentityFixture()
	graph, _ := ix.Neighborhood("run")
	first := graph.GraphJSON()
	for left, right := 0, len(ix.Symbols)-1; left < right; left, right = left+1, right-1 {
		ix.Symbols[left], ix.Symbols[right] = ix.Symbols[right], ix.Symbols[left]
	}
	reversed := exportIdentityFixture()
	reversed.Symbols = ix.Symbols
	reversed.Calls["run"][0], reversed.Calls["run"][1] = reversed.Calls["run"][1], reversed.Calls["run"][0]
	other, _ := reversed.Neighborhood("run")
	if first != other.GraphJSON() || graph.GraphCypher() != other.GraphCypher() || graph.GraphGraphML() != other.GraphGraphML() {
		t.Fatal("graph depends on declaration or call enumeration order")
	}
	for _, limit := range []int{1, 2, 3} {
		capped, _ := ix.Neighborhood("run")
		capped.Cap(limit)
		if len(capped.Nodes) != limit || len(capped.Edges) > limit {
			t.Fatalf("cap exceeded: %+v", capped)
		}
		kept := map[string]bool{}
		for _, node := range capped.Nodes {
			kept[node.ID] = true
		}
		for _, edge := range capped.Edges {
			if !kept[edge.From] || !kept[edge.To] {
				t.Fatalf("dangling edge after cap: %+v", edge)
			}
		}
		for _, root := range capped.RootIDs {
			if !kept[root] {
				t.Fatalf("dangling root after cap: %s", root)
			}
		}
		if len(capped.RootIDs) != min(limit, 2) {
			t.Fatalf("root lost before neighbors: %+v", capped)
		}
	}
}

func TestGraphGraphMLEscapesIdentityAndLabels(t *testing.T) {
	identity := "ref:\"<&>\\"
	graph := GraphResult{Nodes: []GraphNode{{ID: identity, Name: "a<&>"}}, Edges: []GraphEdge{{From: identity, To: identity, Confidence: confHigh}}}
	var decoded struct {
		Graph struct {
			Nodes []struct {
				ID   string `xml:"id,attr"`
				Data []struct {
					Key  string `xml:"key,attr"`
					Text string `xml:",chardata"`
				} `xml:"data"`
			} `xml:"node"`
			Edges []struct {
				From string `xml:"source,attr"`
				To   string `xml:"target,attr"`
			} `xml:"edge"`
		} `xml:"graph"`
	}
	if err := xml.Unmarshal([]byte(graph.GraphGraphML()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Graph.Nodes[0].ID != identity || decoded.Graph.Edges[0].From != identity || decoded.Graph.Edges[0].To != identity {
		t.Fatal("GraphML changed identity while escaping")
	}
	if decoded.Graph.Nodes[0].Data[0].Key != "name" || decoded.Graph.Nodes[0].Data[0].Text != "a<&>" {
		t.Fatal("GraphML lost readable label")
	}
}

func TestGraphIdentitySnapshotRoundTrip(t *testing.T) {
	ix := exportIdentityFixture()
	snapshot, err := ix.Snapshot("subgraph", "run", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := snapshot.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSnapshot(path)
	if err != nil || !reflect.DeepEqual(snapshot.Graph, loaded.Graph) {
		t.Fatalf("snapshot changed graph identity: %v", err)
	}
	snapshot.SchemaVersion = 1
	if err := snapshot.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("old name-identity snapshot accepted")
	}
}

func TestGraphHTMLScriptParsesWithReadableLabels(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	ix := exportIdentityFixture()
	graph, _ := ix.Neighborhood("run")
	html := graph.GraphHTML(ix)
	script := strings.Split(strings.Split(html, "<script>")[1], "</script>")[0]
	command := exec.Command(node, "--check")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("invalid renderer JavaScript: %v\n%s", err, output)
	}
	var encoded GraphResult
	if err := json.Unmarshal([]byte(graph.GraphJSON()), &encoded); err != nil || encoded.SchemaVersion != GraphSchemaVersion {
		t.Fatalf("graph schema missing: %v", err)
	}
	if !strings.Contains(script, "return node.name || node.id") || !strings.Contains(script, "g.root_ids") {
		t.Fatal("renderer uses opaque IDs as labels or root query as identity")
	}
}

func TestGraphExportParsedSourceScopes(t *testing.T) {
	fixtures := []struct {
		name  string
		files map[string]string
		root  string
	}{
		{"go", map[string]string{
			"a/main.go": "package a\nfunc helper() {}\nfunc run() { helper() }\n",
			"b/main.go": "package b\nfunc helper() {}\nfunc run() { helper() }\n",
		}, "run"},
		{"python", map[string]string{
			"a/main.py": "def helper():\n    pass\ndef run():\n    helper()\n",
			"b/main.py": "def helper():\n    pass\ndef run():\n    helper()\n",
		}, "run"},
		{"typescript", map[string]string{
			"a/main.ts": "function helper() {}\nfunction run() { helper(); }\n",
			"b/main.ts": "function helper() {}\nfunction run() { helper(); }\n",
		}, "run"},
		{"java", map[string]string{
			"a/App.java": "public class App {\n public void helper() {}\n public void run() { helper(); }\n}\n",
			"b/App.java": "public class App {\n public void helper() {}\n public void run() { helper(); }\n}\n",
		}, "App.run"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			ix, err := Build(writeTree(t, fixture.files))
			if err != nil {
				t.Fatal(err)
			}
			graph, found := ix.Neighborhood(fixture.root)
			if !found || len(graph.RootIDs) != 2 {
				t.Fatalf("parsed source declarations/calls lost: %+v", graph)
			}
			nodes := map[string]GraphNode{}
			for _, node := range graph.Nodes {
				nodes[node.ID] = node
			}
			helperCalls := 0
			for _, edge := range graph.Edges {
				if edge.Resolution != "resolved" || nodes[edge.From].File != nodes[edge.To].File {
					t.Fatalf("parsed source binding escaped scope: %+v", edge)
				}
				if nodes[edge.To].Name == "helper" || nodes[edge.To].Name == "App.helper" {
					helperCalls++
				}
			}
			if helperCalls != 2 {
				t.Fatalf("expected two source-owned helper calls, got %d", helperCalls)
			}
		})
	}
}

func TestGraphExportRetainsSyntheticUnownedReferences(t *testing.T) {
	ix := &Index{
		Symbols: []Symbol{{Name: "Handler", File: "app.go", Line: 5, Kind: "func", Lang: "go"}},
		Calls: map[string][]CallEdge{
			"route:/hello": {{Target: "Handler", SourceFile: "app.go", Confidence: ConfidenceLow, Synth: "router:net-http"}},
		},
	}
	graph, found := ix.Neighborhood("Handler")
	if !found || len(graph.Edges) != 1 || graph.Edges[0].Synth != "router:net-http" || graph.Edges[0].Resolution != "unresolved" {
		t.Fatalf("unowned synthesized call hidden or fabricated: %+v", graph)
	}
	if graph.Edges[0].To != ix.Symbols[0].DeclarationID() {
		t.Fatalf("synthetic target identity lost: %+v", graph.Edges[0])
	}
	for _, node := range graph.Nodes {
		if node.ID == graph.Edges[0].From && (node.File != "" || node.Line != 0 || node.Resolution != "unresolved") {
			t.Fatalf("synthetic caller assigned a fake declaration: %+v", node)
		}
	}
}

func BenchmarkGraphExportNeighborhood(b *testing.B) {
	ix := exportIdentityFixture()
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		if _, found := ix.Neighborhood("run"); !found {
			b.Fatal("root missing")
		}
	}
}
