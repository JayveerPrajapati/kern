package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/governance"
)

func mkSel(id string, class domain.ContextClass, rel float64) domain.ContextItem {
	return domain.ContextItem{
		ID:        id,
		Class:     class,
		Content:   "content-" + id,
		Relevance: rel,
		Freshness: time.Now(),
		LastUsed:  time.Now(),
	}
}

func containsID(items []domain.ContextItem, id string) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// TestSelectContextAppliesSelectionOrder verifies the documented selection
// order (intent → target/deps → constraints → memory → tests → evidence →
// runtime) is reflected in the returned items when nothing is trimmed.
func TestSelectContextAppliesSelectionOrder(t *testing.T) {
	items := []domain.ContextItem{
		mkSel("runtime", domain.ContextHistory, 0.9),       // stage 7
		mkSel("memory", domain.ContextMemory, 0.6),         // stage 4
		mkSel("intent", domain.ContextUserIntent, 0.9),     // stage 0
		mkSel("constraint", domain.ContextConstraint, 0.2), // stage 3
		mkSel("test", domain.ContextTestResult, 0.7),       // stage 5
		mkSel("state", domain.ContextTaskState, 0.5),       // stage 1
		mkSel("code", domain.ContextSourceCode, 0.8),       // stage 2
		mkSel("evidence", domain.ContextEvidence, 0.4),     // stage 6 (historical)
	}
	req := SelectRequest{Items: items, Threshold: 0} // threshold 0 = keep all
	got := SelectContext(req)

	// Expect stage order 0,1,2,3,4,5,6,7 with no trimming.
	want := []string{"intent", "state", "code", "constraint", "memory", "test", "evidence", "runtime"}
	if len(got) != len(want) {
		t.Fatalf("SelectContext len = %d, want %d (got %+v)", len(got), len(want), got)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
}

// TestSelectContextPermissionsFilter verifies authorization runs FIRST: items
// the holder may not read are excluded from the selection entirely.
func TestSelectContextPermissionsFilter(t *testing.T) {
	// Agent may read "source" but not "context". Per resourceForItem, facts map
	// to "source" (kept) and constraints map to "context" (denied).
	agent := governance.NewAgent("a1", "coder", "code",
		[]governance.Permission{{Resource: "source", Action: "read"}})
	fw := governance.NewFirewall().WithAgents(agent)

	items := []domain.ContextItem{
		mkSel("fact", domain.ContextFact, 0.9),             // source -> allowed
		mkSel("constraint", domain.ContextConstraint, 0.9), // context -> denied
	}
	got := SelectContext(SelectRequest{Items: items, Firewall: fw, Holder: "a1", Threshold: 0})
	if len(got) != 1 {
		t.Fatalf("SelectContext kept %d items, want 1 (unauthorized filtered), got %+v", len(got), got)
	}
	if got[0].ID != "fact" {
		t.Errorf("kept %q, want only the authorized 'fact'", got[0].ID)
	}
}

// TestSelectContextKeepsConstraintsUnderAggressiveReduction verifies that even
// when the active budget (MaxItems) is exhausted, protected constraints and
// evidence are always retained.
func TestSelectContextKeepsConstraintsUnderAggressiveReduction(t *testing.T) {
	items := []domain.ContextItem{
		mkSel("fact", domain.ContextFact, 0.9),             // stage 2 (non-protected)
		mkSel("constraint", domain.ContextConstraint, 0.1), // stage 3 (protected)
		mkSel("evidence", domain.ContextEvidence, 0.1),     // stage 6 (protected)
	}
	req := SelectRequest{Items: items, Threshold: 0, MaxItems: 1}
	got := SelectContext(req)

	if !containsID(got, "constraint") {
		t.Errorf("constraint dropped under MaxItems=1; got %+v", got)
	}
	if !containsID(got, "evidence") {
		t.Errorf("evidence dropped under MaxItems=1; got %+v", got)
	}
	// Both protected items survive even though MaxItems=1 would otherwise cap
	// the active set to one non-protected item.
	if len(got) != 3 {
		t.Errorf("expected constraint + evidence + 1 fact = 3, got %d (%+v)", len(got), got)
	}
}

// TestSelectMinSufficientReduction verifies the engine reduces to the minimum
// sufficient subset: it stops adding non-protected items once the accumulated
// relevance reaches the threshold, while protected constraints are kept.
func TestSelectMinSufficientReduction(t *testing.T) {
	items := []domain.ContextItem{
		mkSel("constraint", domain.ContextConstraint, 0.1), // protected
		mkSel("memory", domain.ContextMemory, 0.9),         // stage 4
		mkSel("testA", domain.ContextTestResult, 0.2),      // stage 5
		mkSel("testB", domain.ContextTestResult, 0.2),      // stage 5
	}
	req := SelectRequest{Items: items, Threshold: 1.0}
	got := SelectContext(req)

	// constraint always kept; memory (0.9) then testA (0.2) cross the 1.0
	// threshold, so testB is trimmed as unnecessary.
	if !containsID(got, "constraint") {
		t.Errorf("constraint missing: %+v", got)
	}
	if containsID(got, "testB") {
		t.Errorf("testB should be trimmed as non-minimal: %+v", got)
	}
	if !containsID(got, "memory") || !containsID(got, "testA") {
		t.Errorf("sufficient items dropped: %+v", got)
	}
}
