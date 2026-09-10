package integration

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/retrieval"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// containsStr reports whether s is present in list.
func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// TestProgressiveDisclosure drives the real L1/L2/L3 retrieval pipeline over
// the fixture index (Run -> Count -> tick) and verifies the disclosure
// invariant: each level strictly increases content detail, previous-level
// content is a subset, and the cache rejects stale (wrong content hash)
// handles.
func TestProgressiveDisclosure(t *testing.T) {
	ix := buildFixtureIndex(t)

	// L1: index summary — minimal info (name/kind/file/token cost) plus a
	// content-hash handle registered in the default registry.
	r1, err := retrieval.Retrieve(ix, retrieval.Options{Query: "Count", Level: retrieval.L1})
	if err != nil {
		t.Fatalf("L1 Retrieve: %v", err)
	}
	if r1.Level != retrieval.L1 || len(r1.Items) == 0 {
		t.Fatalf("L1: level=%d items=%d, want L1 with >= 1 item", r1.Level, len(r1.Items))
	}
	item := r1.Items[0]
	if item.Name != "Count" {
		t.Fatalf("L1 top hit = %q, want Count", item.Name)
	}
	h := item.Handle
	if h == nil {
		t.Fatal("L1 item has no handle")
	}
	if h.Name != "Count" || h.Type != retrieval.TypeSymbol || h.Source == "" {
		t.Errorf("handle metadata: name=%q type=%q source=%q", h.Name, h.Type, h.Source)
	}
	if h.TokenCost <= 0 {
		t.Errorf("handle TokenCost = %d, want > 0", h.TokenCost)
	}
	if h.ContentHash == "" {
		t.Error("handle ContentHash empty; content-hash staleness gate missing")
	}
	// The L1 retrieval registers the handle; Resolve round-trips by ID.
	if got, ok := retrieval.DefaultRegistry.Resolve(h.ID); !ok || got.ID != h.ID {
		t.Error("DefaultRegistry.Resolve failed to round-trip the L1 handle")
	}

	// L2: neighborhood — callers/callees of Count.
	r2, err := retrieval.Retrieve(ix, retrieval.Options{Symbol: "Count", Level: retrieval.L2})
	if err != nil {
		t.Fatalf("L2 Retrieve: %v", err)
	}
	if r2.Detail == nil {
		t.Fatal("L2 Detail is nil")
	}
	if !containsStr(r2.Detail.Callers, "Run") {
		t.Errorf("L2 callers = %v, want Run", r2.Detail.Callers)
	}
	if !containsStr(r2.Detail.Callees, "tick") {
		t.Errorf("L2 callees = %v, want tick", r2.Detail.Callees)
	}

	// L3: verbatim source slice containing Count's definition.
	r3, err := retrieval.Retrieve(ix, retrieval.Options{Symbol: "Count", Level: retrieval.L3})
	if err != nil {
		t.Fatalf("L3 Retrieve: %v", err)
	}
	if r3.Source == nil || r3.Source.Text == "" {
		t.Fatal("L3 Source is nil or empty")
	}
	if !strings.Contains(r3.Source.Text, "func Count") {
		t.Errorf("L3 source does not contain the definition: %q", r3.Source.Text)
	}

	// Escalation: each level strictly increases rendered content detail.
	t1 := tokenize.Count(retrieval.Render(r1))
	t2 := tokenize.Count(retrieval.Render(r2))
	t3 := tokenize.Count(retrieval.Render(r3))
	if !(t1 < t2 && t2 < t3) {
		t.Errorf("escalation tokens not strictly increasing: L1=%d L2=%d L3=%d", t1, t2, t3)
	}
	// Previous-level content is a subset: every level names the symbol, and
	// L3's source still carries the callee L2 reported.
	for i, text := range []string{retrieval.Render(r1), retrieval.Render(r2), retrieval.Render(r3)} {
		if !strings.Contains(text, "Count") {
			t.Errorf("level %d render lost the symbol name", i+1)
		}
	}
	if !strings.Contains(r3.Source.Text, "tick") {
		t.Errorf("L3 source lost callee info reported at L2: %q", r3.Source.Text)
	}

	// Cache: Get/Set round-trip keyed by handle ID, gated by content hash.
	c := retrieval.NewCache(8)
	c.Set(h, "level-1 cached", item.TokenCost)
	got, tok, ok := c.Get(h.ID, h.ContentHash)
	if !ok || got != "level-1 cached" || tok != item.TokenCost {
		t.Errorf("cache round-trip: ok=%v content=%q tokens=%d", ok, got, tok)
	}
	// A stale handle (wrong content hash) is rejected by Get: the cache must
	// never serve edited content as fresh.
	if _, _, ok := c.Get(h.ID, "stale-content-hash"); ok {
		t.Error("cache Get served a handle under a mismatched content hash")
	}
	stale := retrieval.NewHandle(h.Type, h.Name, h.Source, h.Line, h.TokenCost, h.Confidence, "tampered-hash")
	if _, _, ok := c.Get(stale.ID, "tampered-hash"); ok {
		t.Error("cache Get accepted a handle whose stored content hash does not match")
	}
}
