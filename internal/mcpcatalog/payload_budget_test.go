package catalog

import (
	"encoding/json"
	"testing"
)

// TestToolsListPayloadBudget pins the per-session catalogue tax: every MCP
// client pays this serialized payload before its first call. Measured
// 36,483 B on 2026-10-28 for the 32-tool default surface (71% is
// per-parameter documentation, which agents read; there is no $schema /
// additionalProperties boilerplate left to shave, and the phase/risk/category/
// schemaVersion/cacheable fields are protocol-pinned). A conscious addition
// may cross the ceiling — re-measure, then raise this budget deliberately.
func TestToolsListPayloadBudget(t *testing.T) {
	b, err := json.Marshal(All)
	if err != nil {
		t.Fatal(err)
	}
	const budget = 37000
	if len(b) > budget {
		t.Fatalf("tools/list catalogue is %d B, over the %d B per-session budget — trim parameter docs or split the surface, then re-measure and raise the budget deliberately", len(b), budget)
	}
}
