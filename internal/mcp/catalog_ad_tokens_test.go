package mcp

import (
	"encoding/json"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// maxCatalogAdvertisedTokens is the drift gate for the total token cost of
// the tools/list advertisement (every tool's name + description + JSON
// marshaled input schema, concatenated). Every MCP client pays this at
// session start — before any tool is called — so it is a startup-cost budget
// that must not regress.
//
// Measured after the catalog description slim (Lever 1, 2026-09-23):
// the pre-slim advertisement was 24,220 tokens; the post-slim advertisement
// is 17,101 tokens. The cap is the post-slim measurement rounded up with
// ~15% headroom (17,101 x 1.15 = 19,666 -> 19,700), so a slow re-bloat of
// descriptions (roughly +15% over the whole catalog) is caught while normal
// wording tweaks stay under the wire. Re-measured 2026-09-25 (R1 per-surface
// root wording: the ~106 `root` parameter descriptions now state the stdio
// vs web console/SDK confinement surfaces explicitly): 20,904 tokens ->
// cap 20,904 x 1.15 = 24,039 -> 24,100.
const maxCatalogAdvertisedTokens = 24100

// maxToolDescriptionBytes guards against a single bloated description
// sneaking back in: any one tool's description must stay under 300 bytes.
const maxToolDescriptionBytes = 300

// TestCatalogAdvertisementTokenBudget reconstructs exactly what tools/list
// advertises per tool (name + description + JSON-marshaled input schema,
// mirroring the server's payload shape) and asserts the total token count
// stays under the drift-gate cap. It also enforces the per-tool description
// byte ceiling.
func TestCatalogAdvertisementTokenBudget(t *testing.T) {
	t.Parallel()
	total := 0
	for _, tool := range catalog.All {
		schemaBytes, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema for %s: %v", tool.Name, err)
		}
		ad := tool.Name + " " + tool.Description + " " + string(schemaBytes)
		total += tokenize.Count(ad)

		if len(tool.Description) > maxToolDescriptionBytes {
			t.Errorf("tool %s description = %d bytes, exceeds cap %d", tool.Name, len(tool.Description), maxToolDescriptionBytes)
		}
	}
	t.Logf("catalog advertisement total tokens: %d (%d tools)", total, len(catalog.All))
	if total > maxCatalogAdvertisedTokens {
		t.Errorf("catalog advertisement = %d tokens, exceeds drift-gate cap %d — every MCP client pays this at session start",
			total, maxCatalogAdvertisedTokens)
	}
}
