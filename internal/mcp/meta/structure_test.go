package meta_test

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

// TestStructureToolsAreRoutable pins that the structure-router tools are in
// the meta dispatch table (meta.RoutableTools): Handle would otherwise
// silently fall back to kern_search for requests that classify to them.
// The structure classifier itself (ClassifyStructureTools) lives in
// metaroute; its routing pins moved there with it (structure_test.go in
// internal/metaroute).
func TestStructureToolsAreRoutable(t *testing.T) {
	routable := map[string]bool{}
	for _, n := range meta.RoutableTools() {
		routable[n] = true
	}
	for _, n := range []string{"kern_cycles", "kern_verify", "kern_fragility_hotspots"} {
		if !routable[n] {
			t.Errorf("%s is not routable: Handle would silently fall back to kern_search", n)
		}
	}
}
