package intelligence

import (
	"strings"
	"testing"
)

// TestFileNodesAndEdges verifies the graph emits "file" nodes and
// "contains"/"defines" edges linking each file to its symbols.
func TestFileNodesAndEdges(t *testing.T) {
	g := FromIndex(fakeIndex())

	// At least one node has Kind "file".
	hasFile := false
	for _, n := range g.Nodes {
		if n.Kind == "file" {
			hasFile = true
			if n.File == nil || n.File.Path == "" {
				t.Errorf("file node %q has nil/empty File", n.ID)
			}
			if !strings.HasPrefix(n.ID, "file:") {
				t.Errorf("file node ID %q does not start with \"file:\"", n.ID)
			}
		}
	}
	if !hasFile {
		t.Fatal("no node with Kind == \"file\" found")
	}

	// At least one "contains" edge (From starts with "file:") and at least one
	// "defines" edge (To starts with "file:").
	var contains, defines bool
	for _, e := range g.Edges {
		if e.Kind == "contains" && strings.HasPrefix(e.From, "file:") {
			contains = true
		}
		if e.Kind == "defines" && strings.HasPrefix(e.To, "file:") {
			defines = true
		}
	}
	if !contains {
		t.Error("no \"contains\" edge with From starting \"file:\"")
	}
	if !defines {
		t.Error("no \"defines\" edge with To starting \"file:\"")
	}
}

// TestWhatDependsOnExcludesFileNodes ensures the new file nodes do NOT leak into
// the call graph: WhatDependsOn must only return call-graph reachable symbol
// IDs, never "file:*" nodes.
func TestWhatDependsOnExcludesFileNodes(t *testing.T) {
	g := FromIndex(fakeIndex())
	for _, id := range []string{"Foo", "Bar", "Baz", "HandleUsers"} {
		for _, n := range g.WhatDependsOn(id) {
			if n.Kind == "file" || strings.HasPrefix(n.ID, "file:") {
				t.Errorf("WhatDependsOn(%q) returned file node %q; file nodes must not appear as call-graph dependants", id, n.ID)
			}
		}
	}
}
