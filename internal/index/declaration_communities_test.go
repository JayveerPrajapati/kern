package index

import (
	"sort"
	"strings"
	"testing"
)

func TestDeclarationCommunitiesKeepSameNamedDeclarationsApart(t *testing.T) {
	ix, err := Build(writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": "package a\n\nfunc One() { helper() }\n\nfunc helper() {}\n",
		"b/b.go": "package b\n\nfunc Two() { helper() }\n\nfunc helper() {}\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, symbol := range ix.Symbols {
		ids[symbol.File+":"+symbol.Name] = symbol.DeclarationID()
	}
	labels := ix.DeclarationCommunities()
	one, helperA, two, helperB := labels[ids["a/a.go:One"]], labels[ids["a/a.go:helper"]], labels[ids["b/b.go:Two"]], labels[ids["b/b.go:helper"]]
	if one == "" || two == "" || one != helperA || two != helperB || one == two {
		t.Fatalf("same-named helpers were merged across packages: %v", labels)
	}
	members := ix.CommunityMembers(ids["a/a.go:One"])
	if !sort.StringsAreSorted(members) || strings.Join(members, ",") != "One,a.helper" {
		t.Fatalf("members = %v", members)
	}
}

func TestDeclarationNamesDisambiguateOverloads(t *testing.T) {
	ix := &Index{Symbols: []Symbol{
		{Kind: "method", Name: "run", Receiver: "Worker", File: "a/worker.java", Line: 10, Params: []string{"int value"}},
		{Kind: "method", Name: "run", Receiver: "Worker", File: "a/worker.java", Line: 10, Params: []string{"String value"}},
		{Kind: "func", Name: "unique", File: "a/worker.java", Line: 20},
	}}
	names := ix.DeclarationNames()
	seen := map[string]bool{}
	for _, symbol := range ix.Symbols {
		name := names[symbol.DeclarationID()]
		if name == "" || seen[name] {
			t.Fatalf("missing or colliding declaration name: %v", names)
		}
		seen[name] = true
		if symbol.Name == "unique" && name != "unique" {
			t.Fatalf("unique name changed: %q", name)
		}
	}
}
