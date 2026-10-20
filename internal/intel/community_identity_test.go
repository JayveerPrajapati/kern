package intel

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/topology"
)

// TestCommunityReportsKeepDeclarationsSeparate spans both packages: the
// community/coupling reports come from topology, while the graph-community
// members and wiki frontmatter helpers (graphCommunityMembers,
// obsidianFrontmatter) stay in intel.
func TestCommunityReportsKeepDeclarationsSeparate(t *testing.T) {
	ix := buildIndex(t, writeTree(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.22\n",
		"a/a.go": "package a\n\nfunc One() { helper() }\n\nfunc helper() {}\n",
		"b/b.go": "package b\n\nfunc Two() { helper() }\n\nfunc helper() {}\n",
	}))
	communities := topology.Communities(ix)
	if len(communities) != 2 {
		t.Fatalf("same-named helpers merged: %+v", communities)
	}
	for _, community := range communities {
		if community.Size != 2 || len(community.Packages) != 1 || community.Hub != community.Packages[0]+".helper" {
			t.Fatalf("pooled community hub or provenance: %+v", community)
		}
	}
	architecture := topology.AnalyzeArchitecture(ix)
	if len(architecture.Communities) != 2 || len(architecture.Coupling) != 0 {
		t.Fatalf("fabricated architectural coupling: %+v", architecture)
	}
	if members := graphCommunityMembers(ix, "helper"); members != nil {
		t.Fatalf("ambiguous query selected a community: %v", members)
	}
	for _, symbol := range ix.Symbols {
		if symbol.Name == "helper" && symbol.File == "a/a.go" {
			members := graphCommunityMembers(ix, symbol.DeclarationID())
			if !reflect.DeepEqual(members, []string{"One", "a.helper"}) {
				t.Fatalf("wrong declaration community: %v", members)
			}
		}
	}
	ix.Communities = map[string]string{"helper": "wrong"}
	var symbols []index.Symbol
	for _, symbol := range ix.Symbols {
		if symbol.File == "a/a.go" {
			symbols = append(symbols, symbol)
		}
	}
	frontmatter := obsidianFrontmatter(ix, "a", symbols)
	label := ix.DeclarationCommunities()[symbols[0].DeclarationID()]
	if label == "" || !strings.Contains(frontmatter, label) || strings.Contains(frontmatter, "wrong") {
		t.Fatalf("wiki used name-keyed clustering: %s", frontmatter)
	}
}
