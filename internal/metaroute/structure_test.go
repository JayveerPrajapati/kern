package metaroute_test

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/metaroute"
)

func TestClassifyMetaRequest_StructureRouting(t *testing.T) {
	cases := []struct {
		name, request, wantTool string
		wantArgs                map[string]string
	}{
		{"cycles_and_layering", "analyze package dependencies and find import cycles or layering violations", "kern_cycles", nil},
		{"circular", "are there circular dependencies", "kern_cycles", nil},
		{"layering_only", "check for layering violations", "kern_verify", map[string]string{"types": "architecture"}},
		{"boundary_violation", "any boundary violation between packages", "kern_verify", map[string]string{"types": "architecture"}},
		{"perf_hotspots", "find performance hotspots and profile slow paths", "kern_fragility_hotspots", nil},
		{"bottleneck", "where are the bottlenecks", "kern_fragility_hotspots", nil},
		{"fragile", "which code is most fragile", "kern_fragility_hotspots", nil},
		{"route_flow", "how does a request to /v1/loop flow through the system down to storage", "kern_search", map[string]string{"query": "/v1/loop"}},

		// Must keep existing routes.
		{"impact_keeps_route", "what breaks if I change the cycle detector", "kern_impact", nil},
		{"symbol_keeps_route", "how does ImportCycles work", "kern_explore", map[string]string{"symbol": "ImportCycles"}},
		{"flow_no_route_no_symbol", "how does the bundle upload flow work end to end?", "kern_entry_points", nil},
		{"analyze_still_analyze", "analyze adding a new route", "kern_analyze", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, args := metaroute.ClassifyMetaRequest(tc.request)
			if tool != tc.wantTool {
				t.Fatalf("ClassifyMetaRequest(%q) = %q, want %q", tc.request, tool, tc.wantTool)
			}
			for k, want := range tc.wantArgs {
				if got, _ := args[k].(string); got != want {
					t.Errorf("args[%s] = %q, want %q", k, got, want)
				}
			}
		})
	}
}
