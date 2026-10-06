package metaroute

import "testing"

// TestClassifyMetaRequest_ShowContextRoutesToContext is the F3 misrouting
// regression: "show context for X" must route to kern_context, never fall
// through to kern_search — for both CamelCase and bare lowercase symbols.
func TestClassifyMetaRequest_ShowContextRoutesToContext(t *testing.T) {
	tool, args := ClassifyMetaRequest("show context for dispatch")
	if tool != "kern_context" {
		t.Fatalf("ClassifyMetaRequest(show context for dispatch) = %q, want kern_context", tool)
	}
	if sym, _ := args["symbol"].(string); sym != "dispatch" {
		t.Fatalf("args[symbol] = %q, want dispatch", sym)
	}
	tool, args = ClassifyMetaRequest("show context for NewServer")
	if tool != "kern_context" {
		t.Fatalf("ClassifyMetaRequest(show context for NewServer) = %q, want kern_context", tool)
	}
	if sym, _ := args["symbol"].(string); sym != "NewServer" {
		t.Fatalf("args[symbol] = %q, want NewServer", sym)
	}
}
