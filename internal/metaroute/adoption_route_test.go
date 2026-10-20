package metaroute

import "testing"

// TestClassifyMetaRequest_AdoptionKeywords (B6, anti-stall campaign Phase 4):
// the new trigger phrases for the underused tools route to the right
// handlers — cross-session recall phrasings to kern_memory, cache-flavored
// re-ask phrasings to kern_semcache, and pre-edit impact phrasings to
// kern_impact.
func TestClassifyMetaRequest_AdoptionKeywords(t *testing.T) {
	cases := []struct {
		in       string
		wantTool string
		wantArgs map[string]any
	}{
		{"what did we learn about the sandbox", "kern_memory", map[string]any{"action": "recall"}},
		{"recall past lessons about the release", "kern_memory", map[string]any{"action": "recall"}},
		{"previous sessions about the deploy", "kern_memory", map[string]any{"action": "recall"}},
		{"recall what we learned about dispatch", "kern_memory", map[string]any{"action": "recall"}},
		{"is it cached or did I already ask this", "kern_semcache", map[string]any{}},
		{"did I already ask about the dispatch loop", "kern_semcache", map[string]any{}},
		{"before I change the dispatch table", "kern_impact", map[string]any{"change": "before I change the dispatch table"}},
		// The pre-existing memory routes stay untouched.
		{"recall memory about testing", "kern_memory", map[string]any{"action": "recall"}},
		{"what memories do we have about release", "kern_memory", map[string]any{"action": "recall"}},
		// The memory MECHANISM guard still wins over the new recall arm.
		{"where does kern memory store its lessons", "kern_search", nil},
		// "what breaks" (pre-existing) still routes to impact.
		{"what breaks if I change the cycle detector", "kern_impact", map[string]any{"change": "what breaks if I change the cycle detector"}},
	}
	for _, c := range cases {
		tool, args := ClassifyMetaRequest(c.in)
		if tool != c.wantTool {
			t.Errorf("%q -> tool %q, want %q", c.in, tool, c.wantTool)
			continue
		}
		for k, want := range c.wantArgs {
			if got := args[k]; got != want {
				t.Errorf("%q -> args[%q] = %v, want %v", c.in, k, got, want)
			}
		}
	}
}
