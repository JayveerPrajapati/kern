package setup

import "testing"

// setup_presence_test.go — presence-aware display helpers (detectedSet /
// agentPresenceNote): wired surfaces for agents that are not installed on
// the system must be honestly annotated at render time, and non-agent
// surfaces must never be touched.

func TestAgentPresenceNote(t *testing.T) {
	present := map[string]bool{"opencode": true}
	cases := []struct {
		agent string
		want  string
	}{
		{"opencode", ""}, // installed → no annotation
		{"claude", " (agent not installed on this system)"},
		{"codex", " (agent not installed on this system)"},
		{"qoder", " (agent not installed on this system)"},
		{"mcp (project .mcp.json)", ""}, // non-agent surfaces never annotated
		{"gitignore (generated block)", ""},
		{"AGENTS.md rules", ""},
		{"custom adapter", ""}, // not a known agent
		{"", ""},
	}
	for _, c := range cases {
		if got := agentPresenceNote(c.agent, present); got != c.want {
			t.Errorf("agentPresenceNote(%q, present={opencode}) = %q, want %q", c.agent, got, c.want)
		}
	}
}

func TestDetectedSetMatchesDetectAgents(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no host-installed binaries leak into the detection
	dir := t.TempDir()

	got := detectedSet(dir)
	want := map[string]bool{}
	for _, a := range DetectAgents(dir) {
		want[a] = true
	}
	if len(got) != len(want) {
		t.Fatalf("detectedSet = %v, DetectAgents = %v", got, want)
	}
	for a := range want {
		if !got[a] {
			t.Errorf("detectedSet missing %q", a)
		}
	}
}
