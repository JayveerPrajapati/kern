package integration

import (
	"reflect"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/lenses"
)

// TestReviewLenses exercises the real lens registry and application on both
// synthetic claims and a real engine packet: SecurityLens re-ranks policy
// evidence first, BalancedLens never reorders (stable sort, all weights 1.0),
// the builtin registry resolves all five names, and a lensed engine packet
// still renders with its target symbol intact.
func TestReviewLenses(t *testing.T) {
	// Synthetic claims with mixed evidence (policy vs graph vs test).
	claims := []domain.Claim{
		{Statement: "graph: Run depends on Count", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
		{Statement: "policy: external egress requires approval", Evidence: []domain.Evidence{{Type: domain.EvidencePolicy}}},
		{Statement: "test: TestCount covers Count", Evidence: []domain.Evidence{{Type: domain.EvidenceTest}}},
	}

	// SecurityLens re-ranks policy-evidence claims first (policy weight 1.0).
	sec := lenses.ApplyLens(lenses.SecurityLens(), claims)
	if len(sec) != len(claims) {
		t.Fatalf("SecurityLens dropped claims: %d -> %d", len(claims), len(sec))
	}
	if len(sec[0].Evidence) == 0 || sec[0].Evidence[0].Type != domain.EvidencePolicy {
		t.Errorf("SecurityLens first claim evidence = %v, want policy", sec[0].Evidence)
	}

	// BalancedLens (every type weighted 1.0) never reorders: the stable sort
	// keeps original order, so the lensed claims equal the input.
	bal := lenses.ApplyLens(lenses.BalancedLens(), claims)
	if !reflect.DeepEqual(bal, claims) {
		t.Errorf("BalancedLens reordered claims: %v -> %v", claims, bal)
	}

	// Registry resolves all five built-in lens names.
	reg := lenses.NewRegistryWithBuiltins()
	want := []string{"architecture", "balanced", "maintainability", "performance", "security"}
	if got := reg.List(); !reflect.DeepEqual(got, want) {
		t.Errorf("builtin registry list = %v, want %v", got, want)
	}
	for _, name := range want {
		if _, ok := reg.Select(name); !ok {
			t.Errorf("registry missing builtin lens %q", name)
		}
	}

	// Full flow: a real engine packet for the fixture symbol, lensed with
	// SecurityLens, puts the highest-scoring evidence type (policy, 1.0)
	// first; the lensed packet still renders with the symbol name.
	ix := buildFixtureIndex(t)
	eng := newContextEngine(t, ix)
	pkt, err := eng.AnalyzeChange("Count")
	if err != nil {
		t.Fatalf("AnalyzeChange(Count): %v", err)
	}
	if len(pkt.Facts) == 0 {
		t.Fatal("engine packet has no facts to lens")
	}
	lensed := lenses.ApplyLens(lenses.SecurityLens(), pkt.Facts)
	if len(lensed) != len(pkt.Facts) {
		t.Fatalf("ApplyLens dropped facts: %d -> %d", len(pkt.Facts), len(lensed))
	}
	if len(lensed[0].Evidence) == 0 || lensed[0].Evidence[0].Type != domain.EvidencePolicy {
		t.Errorf("lensed first fact evidence = %v, want policy (highest security-lens score)", lensed[0].Evidence)
	}
	// Lensed packet still renders with the symbol name.
	pkt.Facts = lensed
	text := context.RenderText(pkt)
	if !strings.Contains(text, "Count") {
		t.Errorf("RenderText of lensed packet lost symbol: %q", text)
	}
}
