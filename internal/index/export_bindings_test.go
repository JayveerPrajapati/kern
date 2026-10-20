package index

import (
	"reflect"
	"testing"
)

func TestModuleExportsRespectVisibility(t *testing.T) {
	for _, extension := range []string{"js", "ts"} {
		t.Run(extension, func(t *testing.T) {
			ix, err := Build(writeTree(t, map[string]string{
				"lib/core." + extension:  "function renamed() {}\nfunction privateCall() {}\nexport { renamed as publicName };\nexport default function defaultOnly() {}\nexport function visibleCall() {}\nexport class PublicBase {}\nclass PrivateBase {}\nexport function inside() { privateCall(); }\n",
				"app/use." + extension:   "import Default, { publicName as alias, privateCall, defaultOnly, visibleCall } from '../lib/core';\nimport * as ns from '../lib/core';\nexport function use() { Default(); alias(); privateCall(); defaultOnly(); visibleCall(); ns.publicName(); ns.privateCall(); ns.defaultOnly(); ns.visibleCall(); ns.default(); }\n",
				"app/child." + extension: "import { PublicBase, PrivateBase } from '../lib/core';\nexport class Child extends PublicBase {}\nexport class Rejected extends PrivateBase {}\n",
				"lib/peer." + extension:  "export class ImplicitChild extends PublicBase {}\n",
			}))
			if err != nil {
				t.Fatal(err)
			}
			graph, found := ix.Neighborhood("use")
			if !found || len(graph.Edges) != 10 || graph.UnresolvedCalls != 4 {
				t.Fatalf("module visibility was ignored: %+v", graph)
			}
			want := map[string]string{
				"Default": "defaultOnly", "alias": "renamed", "visibleCall": "visibleCall",
				"ns.publicName": "renamed", "ns.visibleCall": "visibleCall", "ns.default": "defaultOnly",
			}
			byID := map[string]GraphNode{}
			for _, node := range graph.Nodes {
				byID[node.ID] = node
			}
			for _, edge := range graph.Edges {
				name, exported := want[edge.Reference]
				if exported {
					if edge.Resolution != "resolved" || byID[edge.To].Name != name || byID[edge.To].File != "lib/core."+extension {
						t.Errorf("export alias bound incorrectly: %+v -> %+v", edge, byID[edge.To])
					}
				} else if edge.Resolution != "unresolved" || len(byID[edge.To].Candidates) != 0 {
					t.Errorf("private declaration leaked through import: %+v", edge)
				}
			}
			local, _ := ix.Neighborhood("inside")
			if len(local.Edges) != 1 || local.UnresolvedCalls != 0 {
				t.Fatalf("same-file private call was lost: %+v", local)
			}
			edges, nodes := ix.InheritBindings()
			for _, node := range nodes {
				byID[node.ID] = node
			}
			seen := map[string]bool{}
			for _, edge := range edges {
				name := byID[edge.From].Name
				seen[name] = true
				if name == "Child" && (edge.Resolution != "resolved" || byID[edge.To].Name != "PublicBase") {
					t.Errorf("exported base did not bind: %+v", edge)
				}
				if name == "Rejected" && (edge.Resolution != "unresolved" || len(byID[edge.To].Candidates) != 0) {
					t.Errorf("private base leaked through import: %+v", edge)
				}
				if name == "ImplicitChild" && (edge.Resolution != "unresolved" || len(byID[edge.To].Candidates) != 0) {
					t.Errorf("same-directory base bound without an import: %+v", edge)
				}
			}
			if !seen["Child"] || !seen["Rejected"] || !seen["ImplicitChild"] {
				t.Fatalf("missing inheritance relationships: %+v", edges)
			}
		})
	}
}

func TestTypeSpaceExportsDoNotBindRuntimeCalls(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"lib/types.ts": "interface Contract {}\ntype Alias = string;\nexport { Contract, Alias };\n",
		"app/use.ts":   "import { Contract, Alias } from '../lib/types';\nexport function use() { Contract(); Alias(); }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	graph, found := ix.Neighborhood("use")
	if !found || len(graph.Edges) != 2 || graph.UnresolvedCalls != 2 {
		t.Fatalf("type-space exports were treated as runtime declarations: %+v", graph)
	}
}

func TestLocalImportedReexportsAndBarrelVisibility(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"lib/origin.ts": "export function origin() {}\nfunction privateCall() {}\n",
		"lib/bridge.ts": "import { origin as local } from './origin';\nexport { local as publicName, local as default };\n",
		"lib/barrel.ts": "export { publicName as again } from './bridge';\nexport * from './origin';\n",
		"lib/cycle1.ts": "export * from './cycle2';\n",
		"lib/cycle2.ts": "export * from './cycle1';\n",
		"app/use.ts":    "import Direct, { publicName } from '../lib/bridge';\nimport { again } from '../lib/barrel';\nimport * as ns from '../lib/barrel';\nimport { missing } from '../lib/cycle1';\nexport function use() { Direct(); publicName(); again(); ns.origin(); ns.privateCall(); ns.default(); missing(); }\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	graph, found := ix.Neighborhood("use")
	if !found || len(graph.Edges) != 7 || graph.UnresolvedCalls != 3 {
		t.Fatalf("local/barrel exports were bound incorrectly: %+v", graph)
	}
	byID := map[string]GraphNode{}
	for _, node := range graph.Nodes {
		byID[node.ID] = node
	}
	for _, edge := range graph.Edges {
		if edge.Resolution == "resolved" && byID[edge.To].File != "lib/origin.ts" {
			t.Errorf("barrel selected a wrong declaration: %+v", edge)
		}
	}
}

func TestDefaultAndReexportBindings(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"lib/core.ts":       "export default function defaultCall() {}\n",
		"lib/impl.ts":       "export function helper() {}\n",
		"lib/star.ts":       "export function starred() {}\n",
		"lib/barrel.ts":     "export { helper as aliased } from './impl';\nexport * from './star';\nexport { default as Core } from './core';\n",
		"app/use.ts":        "import Direct from '../lib/core';\nimport { aliased, starred, Core } from '../lib/barrel';\nexport function use() { Direct(); aliased(); starred(); Core(); }\n",
		"other/core.ts":     "export default function defaultCall() {}\n",
		"app/inherit.ts":    "import Base from '../lib/base';\nexport class Child extends Base {}\n",
		"lib/base.ts":       "export default class Base {}\n",
		"unrelated/base.ts": "export class Base {}\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	graph, found := ix.Neighborhood("use")
	if !found || graph.UnresolvedCalls != 0 || len(graph.Edges) != 4 {
		t.Fatalf("default/reexport calls did not all bind: %+v", graph)
	}
	files := map[string]GraphNode{}
	for _, node := range graph.Nodes {
		files[node.ID] = node
	}
	want := map[string]int{"lib/core.ts": 2, "lib/impl.ts": 1, "lib/star.ts": 1}
	got := map[string]int{}
	for _, edge := range graph.Edges {
		got[files[edge.To].File]++
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls bound to %v, want %v", got, want)
	}
	edges, nodes := ix.InheritBindings()
	byID := map[string]GraphNode{}
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for _, edge := range edges {
		if byID[edge.From].Name == "Child" && (edge.Resolution != "resolved" || byID[edge.To].File != "lib/base.ts") {
			t.Fatalf("default-imported base did not bind: %+v -> %+v", edge, byID[edge.To])
		}
	}
}
