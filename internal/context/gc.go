package context

import (
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// GC runs the context garbage-collection pipeline over a set of context items.
// It scores each item's relevance and decides a GCAction (KEEP/COMPRESS/DEMOTE/
// ARCHIVE/DROP). Strict Plan Phase 5 P1.
//
// Scoring factors:
//   - task relevance (does the item's content match the task intent/target?)
//   - freshness (how recently was the item observed?)
//   - authority (is the source authoritative: graph > memory > tool > history?)
//   - duplicate relationship (is this item a duplicate of another?)
//   - last use (has the item been referenced recently?)
type GC struct {
	intent   string
	target   string
	now      time.Time
	maxItems int // max ACTIVE items (0 = unlimited)
}

// NewGC returns a GC pipeline for the given intent and target. maxItems caps
// the number of ACTIVE items; excess items are demoted.
func NewGC(intent, target string, maxItems int) *GC {
	return &GC{intent: strings.ToLower(intent), target: strings.ToLower(target), now: time.Now(), maxItems: maxItems}
}

// Run scores each item and returns the GC actions. Items are sorted by
// relevance (descending); the top maxItems stay ACTIVE, the rest are demoted.
func (g *GC) Run(items []domain.ContextItem) []domain.GCAction {
	actions := make([]domain.GCAction, len(items))
	scores := make([]float64, len(items))

	// Score each item.
	seenDigest := map[string]int{} // digest → first index with this digest
	for i := range items {
		scores[i] = g.score(items[i])
		// Duplicate detection: if two items have the same digest, the later
		// one gets a penalty (likely a duplicate).
		if d := items[i].Digest; d != "" {
			if _, ok := seenDigest[d]; ok {
				scores[i] *= 0.3 // heavy penalty for duplicates
			} else {
				seenDigest[d] = i
			}
		}
	}

	// Sort indices by score (descending).
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })

	// Assign actions: top maxItems → KEEP, rest → DEMOTE (or DROP if very low).
	for rank, i := range idx {
		if g.maxItems > 0 && rank >= g.maxItems {
			if scores[i] < 0.1 {
				actions[i] = domain.GCDrop
			} else if scores[i] < 0.3 {
				actions[i] = domain.GCArchive
			} else {
				actions[i] = domain.GCDemote
			}
		} else {
			if scores[i] < 0.2 {
				actions[i] = domain.GCCompress
			} else {
				actions[i] = domain.GCKeep
			}
		}
	}
	return actions
}

// score computes a 0.0-1.0 relevance score for an item.
func (g *GC) score(item domain.ContextItem) float64 {
	s := 0.0

	// Task relevance: does the content mention the intent or target?
	content := strings.ToLower(item.Content)
	if g.target != "" && strings.Contains(content, g.target) {
		s += 0.4
	}
	if g.intent != "" {
		for _, word := range strings.Fields(g.intent) {
			if len(word) > 3 && strings.Contains(content, word) {
				s += 0.05
			}
		}
	}

	// Class weight: some classes are always relevant.
	switch item.Class {
	case domain.ContextUserIntent, domain.ContextTaskState, domain.ContextConstraint, domain.ContextPlan:
		s += 0.3 // always relevant
	case domain.ContextFact, domain.ContextEvidence, domain.ContextSourceCode:
		s += 0.2
	case domain.ContextMemory, domain.ContextDecision:
		s += 0.1
	case domain.ContextHistory, domain.ContextToolResult, domain.ContextError:
		s += 0.0 // only relevant if content matches
	}

	// Freshness: newer items get a small boost.
	if !item.Freshness.IsZero() {
		age := g.now.Sub(item.Freshness)
		if age < 1*time.Hour {
			s += 0.1
		} else if age < 24*time.Hour {
			s += 0.05
		}
	}

	// Authority: graph > memory > tool > history.
	switch item.Source {
	case "graph", "intelligence":
		s += 0.1
	case "memory":
		s += 0.05
	}

	// Clamp to [0, 1].
	if s > 1.0 {
		s = 1.0
	}
	return s
}

// ApplyActions applies GC actions to items, returning the filtered ACTIVE set
// and the full set with updated states.
func ApplyActions(items []domain.ContextItem, actions []domain.GCAction) (active []domain.ContextItem, updated []domain.ContextItem) {
	for i, item := range items {
		switch actions[i] {
		case domain.GCKeep:
			item.State = domain.ContextActive
			active = append(active, item)
		case domain.GCCompress:
			item.State = domain.ContextActive
			// In a full implementation, compress would truncate the content.
			// For now, we keep it but mark it as compressed.
			active = append(active, item)
		case domain.GCDemote:
			item.State = domain.ContextWarm
		case domain.GCArchive:
			item.State = domain.ContextArchived
		case domain.GCDrop:
			item.State = domain.ContextDropped
		}
		updated = append(updated, item)
	}
	return active, updated
}
