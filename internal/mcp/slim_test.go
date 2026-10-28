package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// slimBudgetBytes is the ceiling the audit set for a tools/list payload that an
// agent has to carry in every session.
const slimBudgetBytes = 8000

// toolsListBytes marshals the tool list the way tools/list does.
func toolsListBytes(t *testing.T, ts []Tool) (int, []byte) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"tools": ts})
	if err != nil {
		t.Fatalf("marshal tools: %v", err)
	}
	return len(raw), raw
}

// TestSlimToolsFitsBudget pins P4.1: the compact advertisement is under 8 KB and
// still callable — every tool name survives, every parameter name survives, the
// required list survives, and the annotations used by host confirmation UX
// survive. Only prose is dropped.
func TestSlimToolsFitsBudget(t *testing.T) {
	full := slimIfRequested(tools) // env unset → unchanged
	fullSize, _ := toolsListBytes(t, full)
	slim := slimTools(tools)
	slimSize, raw := toolsListBytes(t, slim)

	if fullSize <= slimBudgetBytes {
		t.Fatalf("the full advertisement is already %d B (≤ %d): the slim gate no longer proves anything", fullSize, slimBudgetBytes)
	}
	if slimSize >= slimBudgetBytes {
		t.Fatalf("slim tools/list is %d B, budget is %d", slimSize, slimBudgetBytes)
	}
	t.Logf("tools/list: full %d B → slim %d B for %d tools", fullSize, slimSize, len(slim))

	// The call contract must be intact: name → {params, required, annotations}.
	var slimDoc struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema map[string]any  `json:"inputSchema"`
			Annotations map[string]bool `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &slimDoc); err != nil {
		t.Fatalf("slim tools/list is not JSON: %v", err)
	}
	if len(slimDoc.Tools) != len(slimSurface) {
		t.Fatalf("slim advertised %d tools, want the %d-tool core surface", len(slimDoc.Tools), len(slimSurface))
	}
	// The router must stay advertised: it is how the unadvertised 24 are reached.
	var router bool
	for _, got := range slimDoc.Tools {
		if got.Name == "kern_meta" && strings.Contains(got.Description, "Routes to every kern tool") {
			router = true
		}
	}
	if !router {
		t.Fatal("kern_meta must be advertised in slim mode with its routing hint")
	}
	byName := map[string]map[string]any{}
	for _, tl := range tools {
		byName[tl.Name] = tl.InputSchema
	}
	for _, got := range slimDoc.Tools {
		orig, ok := byName[got.Name]
		if !ok {
			t.Fatalf("slim invented tool %q", got.Name)
		}
		for _, key := range []string{"properties", "required"} {
			if _, want := orig[key]; want {
				if _, have := got.InputSchema[key]; !have {
					t.Fatalf("%s: slim dropped %q from inputSchema", got.Name, key)
				}
			}
		}
	}
}

// TestFirstSentence pins the clipping used by slim mode: the first sentence
// when there is one, a hard clip otherwise, never an empty description.
func TestFirstSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Search symbols. More detail here.", "Search symbols."},
		{"No terminator at all", "No terminator at all"},
		{"", ""},
	}
	for _, c := range cases {
		if got := firstSentence(c.in, 140); got != c.want {
			t.Errorf("firstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := ""
	for len(long) < 300 {
		long += "word "
	}
	if got := firstSentence(long, 140); len(got) > 145 {
		t.Errorf("long description not clipped: %d bytes", len(got))
	}
}

// TestSlimModeEnvWiring pins the opt-in switch: unset leaves the list
// byte-identical, KERN_MCP_SLIM=1 compacts it.
func TestSlimModeEnvWiring(t *testing.T) {
	t.Setenv("KERN_MCP_SLIM", "")
	plain := slimIfRequested(tools)
	if _, raw := toolsListBytes(t, plain); len(raw) == 0 {
		t.Fatal("unset slim mode produced no tools")
	}
	t.Setenv("KERN_MCP_SLIM", "1")
	compact := slimIfRequested(tools)
	plainSize, _ := toolsListBytes(t, plain)
	compactSize, _ := toolsListBytes(t, compact)
	if compactSize >= plainSize {
		t.Fatalf("KERN_MCP_SLIM=1 did not compact: %d vs %d", compactSize, plainSize)
	}
}
