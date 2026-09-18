package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestGCPinnedSurvivesMaxItemsOne(t *testing.T) {
	now := time.Now()
	// high outranks low on every scoring factor: it matches the target, has a
	// fresh timestamp, and is a directly-related fact.
	high := domain.ContextItem{ID: "high", Class: domain.ContextFact, Content: "ZZZ relevant", Freshness: now, LastUsed: now}
	// low matches nothing: target miss, error class, old freshness.
	low := domain.ContextItem{ID: "low", Class: domain.ContextError, Content: "unrelated noise", Freshness: now.Add(-48 * time.Hour), LastUsed: now.Add(-48 * time.Hour)}

	items := []domain.ContextItem{high, low}
	// maxItems=1, and "low" is pinned despite its near-zero score.
	g := NewGC("", "ZZZ", 1).Pin("low")

	actions := g.Run(items)
	idxHigh := indexOfItemID(items, "high")
	idxLow := indexOfItemID(items, "low")

	if actions[idxLow] != domain.GCKeep {
		t.Errorf("pinned low item action = %q, want KEEP (hard-pin must survive)", actions[idxLow])
	}
	if actions[idxHigh] != domain.GCKeep {
		t.Errorf("high item action = %q, want KEEP", actions[idxHigh])
	}

	// The pinned item must also survive into the ACTIVE set after ApplyActions.
	active, _ := ApplyActions(items, actions)
	if !hasItemID(active, "low") {
		t.Errorf("pinned low item missing from active set; active = %+v", active)
	}
}

func TestGCPinPreventsDrop(t *testing.T) {
	now := time.Now()
	dropCandidate := domain.ContextItem{ID: "doomed", Class: domain.ContextHistory, Content: "stale unrelated", Freshness: now.Add(-72 * time.Hour), LastUsed: now.Add(-72 * time.Hour)}
	winner := domain.ContextItem{ID: "winner", Class: domain.ContextFact, Content: "ZZZ on target", Freshness: now, LastUsed: now}

	items := []domain.ContextItem{dropCandidate, winner}
	// Without the pin the dropCandidate scores ~0 and lands beyond maxItems=1:
	// it would be DROPPED.
	gPlain := NewGC("", "ZZZ", 1)
	if act := gPlain.Run(items)[indexOfItemID(items, "doomed")]; act != domain.GCDrop {
		t.Fatalf("precondition: unpinned doomed item action = %q, want GCDrop", act)
	}

	// With the pin it must survive.
	gPinned := NewGC("", "ZZZ", 1).Pin("doomed")
	actions := gPinned.Run(items)
	if act := actions[indexOfItemID(items, "doomed")]; act != domain.GCKeep {
		t.Errorf("pinned doomed item action = %q, want GCKeep", act)
	}
}

func TestGCPinnedSurvivesBeyondMaxItems(t *testing.T) {
	now := time.Now()
	// Two pinned items, all low-scoring, against maxItems=1.
	items := []domain.ContextItem{
		{ID: "pin-a", Class: domain.ContextHistory, Content: "a", Freshness: now.Add(-72 * time.Hour), LastUsed: now.Add(-72 * time.Hour)},
		{ID: "pin-b", Class: domain.ContextHistory, Content: "b", Freshness: now.Add(-72 * time.Hour), LastUsed: now.Add(-72 * time.Hour)},
		{ID: "unpin", Class: domain.ContextFact, Content: "ZZZ winner", Freshness: now, LastUsed: now},
	}

	g := NewGC("", "ZZZ", 1).Pin("pin-a", "pin-b")
	actions := g.Run(items)

	for _, id := range []string{"pin-a", "pin-b"} {
		if act := actions[indexOfItemID(items, id)]; act != domain.GCKeep {
			t.Errorf("pinned item %q action = %q, want GCKeep even though pinned count (2) > maxItems (1)", id, act)
		}
	}
}

func TestGCPinPreventsDemote(t *testing.T) {
	now := time.Now()
	// mid scores just under the winner: beyond maxItems=1 it would be demoted.
	mid := domain.ContextItem{ID: "mid", Class: domain.ContextFact, Content: "ZZZ", Freshness: now.Add(-24 * time.Hour), LastUsed: now.Add(-24 * time.Hour)}
	winner := domain.ContextItem{ID: "winner", Class: domain.ContextFact, Content: "ZZZ fresh and direct", Freshness: now, LastUsed: now}

	items := []domain.ContextItem{mid, winner}
	gPlain := NewGC("", "ZZZ", 1)
	plainAct := gPlain.Run(items)[indexOfItemID(items, "mid")]
	if plainAct == domain.GCKeep {
		t.Fatalf("precondition: unpinned mid item should not be KEEP, got %q", plainAct)
	}

	gPinned := NewGC("", "ZZZ", 1).Pin("mid")
	if act := gPinned.Run(items)[indexOfItemID(items, "mid")]; act != domain.GCKeep {
		t.Errorf("pinned mid item action = %q, want GCKeep (no demote)", act)
	}
}

func TestGCPinOptInZeroValue(t *testing.T) {
	now := time.Now()
	near := domain.ContextItem{ID: "near", Class: domain.ContextFact, Content: "x", Freshness: now, LastUsed: now}
	far := domain.ContextItem{ID: "far", Class: domain.ContextFact, Content: "x", Freshness: now, LastUsed: now}
	items := []domain.ContextItem{near, far}

	// Same scenario as TestGCDependencyDistance: without Pin, the far item is
	// still demoted/dropped.
	g := NewGC("", "ZZZ", 1).SetDependencyDistance(map[string]int{"near": 0, "far": 10})
	actions := g.Run(items)
	if act := actions[indexOfItemID(items, "near")]; act != domain.GCKeep {
		t.Errorf("near item action = %q, want KEEP", act)
	}
	if act := actions[indexOfItemID(items, "far")]; act == domain.GCKeep {
		t.Errorf("far item should be demoted/dropped when nothing is pinned, got %q", act)
	}
}

func hasItemID(items []domain.ContextItem, id string) bool {
	return indexOfItemID(items, id) >= 0
}
