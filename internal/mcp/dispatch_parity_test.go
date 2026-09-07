package mcp

import "testing"

// TestDispatchParityWithRegistration is the dispatch<->registration
// invariant: every registered kern_* tool must have a dispatch entry in
// dispatchTable, and every dispatch entry must name a registered tool. Dead
// arms (entries for unregistered tools) rot; missing entries make registered
// tools unreachable. The table is the single dispatch mechanism — no source
// parsing needed, the check is structural.
func TestDispatchParityWithRegistration(t *testing.T) {
	registered := map[string]bool{}
	for _, n := range ToolNames() {
		registered[n] = true
	}
	if len(registered) < 40 {
		t.Fatalf("suspiciously small MCP catalog: %d tools", len(registered))
	}

	dispatched := map[string]bool{}
	for name := range dispatchTable {
		dispatched[name] = true
	}
	if len(dispatched) < 40 {
		t.Fatalf("suspiciously few dispatch entries: %d", len(dispatched))
	}

	for name := range dispatched {
		if !registered[name] {
			t.Errorf("dispatch entry %s has no registered tool — dead arm", name)
		}
	}
	for name := range registered {
		if !dispatched[name] {
			t.Errorf("registered tool %s has no dispatch entry — unreachable", name)
		}
	}
	if len(dispatched) != len(registered) {
		t.Errorf("dispatch/registration mismatch: %d dispatched vs %d registered", len(dispatched), len(registered))
	}
}

// TestCatalogCount verifies that the total registered MCP tools match the canonical catalog size.
func TestCatalogCount(t *testing.T) {
	names := ToolNames()
	if len(names) != len(tools) {
		t.Fatalf("ToolNames() count %d != tools table count %d", len(names), len(tools))
	}
	if len(names) < 104 {
		t.Fatalf("MCP tool catalog has shrunk below expected 104 tools: got %d", len(names))
	}
}
