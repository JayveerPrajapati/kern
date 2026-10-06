package toolsurface

import "testing"

func TestIsDefault(t *testing.T) {
	for _, name := range Default {
		if !IsDefault(name) {
			t.Errorf("IsDefault(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"kern_graph", "kern_exec", "kern_validate", "kern_doc", "kern_stats", "not_a_tool"} {
		if IsDefault(name) {
			t.Errorf("IsDefault(%q) = true, want false", name)
		}
	}
}

func TestAnnotate(t *testing.T) {
	got := Annotate([]string{"kern_explore", "kern_graph", "kern_verify", "kern_exec"})
	want := "kern_explore, kern_graph (KERN_MCP_FULL), kern_verify, kern_exec (KERN_MCP_FULL)"
	if got != want {
		t.Fatalf("Annotate = %q, want %q", got, want)
	}
	if got := Annotate(nil); got != "" {
		t.Fatalf("Annotate(nil) = %q, want empty", got)
	}
}

func TestSetMatchesDefault(t *testing.T) {
	m := Set()
	if len(m) != len(Default) {
		t.Fatalf("Set size = %d, Default size = %d", len(m), len(Default))
	}
	for _, n := range Default {
		if !m[n] {
			t.Errorf("Set() missing %q", n)
		}
	}
}
