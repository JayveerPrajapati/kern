package topology

import "testing"

// The render tests in this file moved from internal/intel/intel_render_test.go
// with the arch/communities/hubs family; they use the buildTestProject /
// buildIndex / containsStr fixture copies in fixtures_test.go.

func TestRenderArch(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderArch(AnalyzeArchitecture(ix))
	if !containsStr(out, "communities") || !containsStr(out, "coupling") {
		t.Fatalf("expected architecture overview, got %q", out)
	}
}

func TestRenderCommunities(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	out := RenderCommunities(Communities(ix))
	if !containsStr(out, "community") && !containsStr(out, "cluster") {
		t.Fatalf("expected communities output, got %q", out)
	}
}

func TestRenderHubsAndBridges(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)
	hubs := RenderHubs(Hubs(ix, 10))
	if !containsStr(hubs, "hub") {
		t.Fatalf("expected hubs output, got %q", hubs)
	}
	bridges := RenderBridges(Bridges(ix, 15))
	if bridges == "" {
		t.Fatal("expected bridges output")
	}
}
