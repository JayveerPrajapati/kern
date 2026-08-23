package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// TestGCDependencyDistance tests the P5.5 dependency_distance factor: an item
// whose target is far from the task target in the dependency graph is demoted
// or dropped before a near item of equal base relevance.
func TestGCDependencyDistance(t *testing.T) {
	now := time.Now()
	// Two identical items (same class, same content, same freshness, same last
	// use) so every other factor cancels out; they differ only in dependency
	// distance from the task target.
	near := domain.ContextItem{ID: "near", Class: domain.ContextFact, Content: "x", Freshness: now, LastUsed: now}
	far := domain.ContextItem{ID: "far", Class: domain.ContextFact, Content: "x", Freshness: now, LastUsed: now}

	items := []domain.ContextItem{near, far}
	// maxItems=1: only the higher-scoring item stays ACTIVE.
	g := NewGC("", "ZZZ", 1).SetDependencyDistance(map[string]int{"near": 0, "far": 10})

	actions := g.Run(items)
	idxNear := indexOfItemID(items, "near")
	idxFar := indexOfItemID(items, "far")

	if actions[idxNear] != domain.GCKeep {
		t.Errorf("near item action = %q, want KEEP", actions[idxNear])
	}
	if actions[idxFar] == domain.GCKeep {
		t.Errorf("far item should be demoted/dropped, got %q", actions[idxFar])
	}
	if g.score(near) <= g.score(far) {
		t.Errorf("near item score (%f) should exceed far item score (%f)", g.score(near), g.score(far))
	}
}

// TestGCTaskRelation tests the P5.5 task_relation factor: an item directly
// related to the current task outranks an unrelated item of equal relevance.
func TestGCTaskRelation(t *testing.T) {
	now := time.Now()
	related := domain.ContextItem{ID: "rel", Class: domain.ContextFact, Content: "x", Freshness: now, LastUsed: now}
	unrelated := domain.ContextItem{ID: "unrel", Class: domain.ContextFact, Content: "x", Freshness: now, LastUsed: now}

	items := []domain.ContextItem{related, unrelated}
	// maxItems=1: only the higher-scoring item stays ACTIVE.
	g := NewGC("", "ZZZ", 1).SetTaskRelation(map[string]float64{"rel": 1.0, "unrel": 0.0})

	actions := g.Run(items)
	idxRel := indexOfItemID(items, "rel")
	idxUnrel := indexOfItemID(items, "unrel")

	if actions[idxRel] != domain.GCKeep {
		t.Errorf("related item action = %q, want KEEP", actions[idxRel])
	}
	if actions[idxUnrel] == domain.GCKeep {
		t.Errorf("unrelated item of equal relevance should not be KEEP, got %q", actions[idxUnrel])
	}
	if g.score(related) <= g.score(unrelated) {
		t.Errorf("related item score (%f) should exceed unrelated score (%f)", g.score(related), g.score(unrelated))
	}
}

func indexOfItemID(items []domain.ContextItem, id string) int {
	for i, it := range items {
		if it.ID == id {
			return i
		}
	}
	return -1
}
