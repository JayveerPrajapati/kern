package main

import "testing"

// TestParseSetupFlags is the setup family's remaining wiring test in
// cmd/kern: the shared unified parser (flags.go) must know the setup flags
// the dispatch table documents. The setup family's behavioral tests
// (styleFor matrix, box rendering, remove flow, summary derivation) moved to
// internal/setup with the extracted logic (workstream 3.6-A — setup family
// extraction).
func TestParseSetupFlags(t *testing.T) {
	f, _, err := parseFlags([]string{"--all-agents", "--plain", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.allAgents || !f.plain || !f.yes {
		t.Errorf("flags not parsed: allAgents=%v plain=%v yes=%v", f.allAgents, f.plain, f.yes)
	}
}
