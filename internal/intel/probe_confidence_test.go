package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// B2 (QA 2026-10-15): prose-only tasks anchor noise keywords fuzzily (the
// word "test" matching Test* functions, "service" matching Service* names).
// Fuzzy anchors must be labeled as low confidence so they never read as
// confident identifications.

func TestProbeLabelsFuzzyAnchorsLowConfidence(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "decommissionNetworkService", File: "net.go", Line: 10},
			{Kind: "func", Name: "NetworkClient", File: "client.go", Line: 20},
			{Kind: "func", Name: "ServiceRegistry", File: "registry.go", Line: 30},
			{Kind: "func", Name: "unrelated", File: "other.go", Line: 40},
		},
		Calls:   map[string][]index.CallEdge{},
		Callers: map[string][]string{},
	}
	r := Probe(ix, "decommission a network service", 4000)
	if len(r.Anchors) == 0 {
		t.Fatal("expected fuzzy-matched anchors for natural-language task, got none")
	}
	for _, a := range r.Anchors {
		if !a.Fuzzy {
			t.Errorf("prose-only anchor %s must be labeled fuzzy (low confidence), got %+v", a.Resolved, a)
		}
	}
	out := RenderProbe(r)
	if !strings.Contains(out, "no exact symbol matches") {
		t.Errorf("render must warn when all anchors are fuzzy, got:\n%s", out)
	}
	if !strings.Contains(out, "[fuzzy match") {
		t.Errorf("render must label each fuzzy anchor, got:\n%s", out)
	}
}

func TestProbeExactAnchorsNotLabeledFuzzy(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "Add", File: "math.go", Line: 10},
		},
		Calls:   map[string][]index.CallEdge{},
		Callers: map[string][]string{},
	}
	r := Probe(ix, "Add returns wrong result", 4000)
	if len(r.Anchors) != 1 || r.Anchors[0].Name != "Add" {
		t.Fatalf("expected exactly one anchor for Add, got %+v", r.Anchors)
	}
	if r.Anchors[0].Fuzzy {
		t.Error("exact anchor Add must not be labeled fuzzy")
	}
	if out := RenderProbe(r); strings.Contains(out, "no exact symbol matches") {
		t.Errorf("exact anchors must not trigger the all-fuzzy warning, got:\n%s", out)
	}
}

func TestProbeCamelCasePartialFallbackLabeledFuzzy(t *testing.T) {
	ix := &index.Index{
		Symbols: []index.Symbol{
			{Kind: "func", Name: "RetryPolicy", File: "svc.go", Line: 10},
			{Kind: "func", Name: "HandleRequest", File: "svc.go", Line: 15},
		},
		Calls:   map[string][]index.CallEdge{},
		Callers: map[string][]string{},
	}
	// HandleRequest resolves exactly; retryPolicy (case mismatch) only
	// anchors through the F4 partial fuzzy fallback — it must be labeled,
	// while the exact anchor stays unlabeled and mixed anchors do not
	// trigger the all-fuzzy warning.
	r := Probe(ix, "retryPolicy explodes when HandleRequest fails", 4000)
	labeled := map[string]bool{}
	for _, a := range r.Anchors {
		labeled[a.Resolved] = a.Fuzzy
	}
	if !labeled["RetryPolicy"] {
		t.Errorf("fuzzy-anchored RetryPolicy must be labeled, anchors: %+v", r.Anchors)
	}
	if labeled["HandleRequest"] {
		t.Errorf("exact HandleRequest must not be labeled, anchors: %+v", r.Anchors)
	}
	if out := RenderProbe(r); strings.Contains(out, "no exact symbol matches") {
		t.Errorf("mixed exact+fuzzy anchors must not trigger the all-fuzzy warning, got:\n%s", out)
	}
}
