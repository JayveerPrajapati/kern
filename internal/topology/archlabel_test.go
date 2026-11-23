package topology

import (
	"strings"
	"testing"
)

// The architecture view used to label a community by an arbitrary member
// symbol ("Anchor"), which said nothing about the subsystem. The label must
// name the dominant package family instead (2026-11-10 campaign).
func TestCommunityLabelNamesPackageFamilies(t *testing.T) {
	c := ArchCommunity{
		ID: "Anchor",
		Packages: []string{
			"internal/mcp", "internal/mcp/graph", "internal/mcp/exec", "internal/mcp/doc",
			"internal/intel",
		},
	}
	got := communityLabel(c)
	if !strings.HasPrefix(got, "internal/mcp") {
		t.Fatalf("label = %q, want the dominant family internal/mcp", got)
	}
	if got == "Anchor" {
		t.Fatal("label fell back to the arbitrary community id despite package metadata")
	}
	if c := (ArchCommunity{ID: "X"}); communityLabel(c) != "X" {
		t.Fatalf("no packages: label = %q, want the id fallback", communityLabel(c))
	}
}

// RenderArch must show the derived labels in both the community rows and the
// coupling rows, and never the raw symbol id when metadata exists.
func TestRenderArchUsesDerivedLabels(t *testing.T) {
	a := Architecture{
		Communities: []ArchCommunity{
			{ID: "Anchor", Size: 3, Hub: "ArgString", Packages: []string{"internal/mcp", "internal/mcp/graph"}},
			{ID: "Cache", Size: 2, Hub: "Get", Packages: []string{"internal/cache"}},
		},
		Coupling: []CouplingEdge{{From: "Anchor", To: "Cache", Count: 4}},
	}
	out := RenderArch(a)
	if strings.Contains(out, "Anchor") || strings.Contains(out, "Cache  ") {
		t.Fatalf("render still shows raw community ids:\n%s", out)
	}
	for _, want := range []string{"internal/mcp", "internal/cache", "4 edges"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}
