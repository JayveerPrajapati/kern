package langparse

import (
	"slices"
	"strings"
)

// dedupeCallEdges dedupes a call-edge slice by target and source file keeping the
// HIGHEST-confidence representative. A first-wins dedupe let an
// edge recorded LOW before it was re-resolved stay LOW forever; promotion
// can also merge distinct target forms ("db.Open" and "Open") into one key,
// and the highest confidence is the honest verdict for that key. Ties keep
// the first occurrence, preserving input order determinism. The output is
// sorted by target then source file, so Calls stays in the deterministic order the
// finalize passes rely on.
func DedupeCallEdges(in []CallEdge) []CallEdge {
	if len(in) < 2 {
		return in
	}
	type edgeKey struct {
		target                string
		file                  string
		dynamic               bool
		synth                 string
		fromID, toID, binding string
	}
	best := map[edgeKey]CallEdge{}
	for _, e := range in {
		key := edgeKey{target: e.Target, file: e.SourceFile, dynamic: e.Dynamic, synth: e.Synth, fromID: e.FromID, toID: e.ToID, binding: e.Binding}
		if prev, ok := best[key]; !ok || ConfRank(e.Confidence) > ConfRank(prev.Confidence) {
			best[key] = e
		}
	}
	out := make([]CallEdge, 0, len(best))
	for _, e := range best {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b CallEdge) int {
		if order := strings.Compare(a.Target, b.Target); order != 0 {
			return order
		}
		if order := strings.Compare(a.SourceFile, b.SourceFile); order != 0 {
			return order
		}
		if a.Dynamic != b.Dynamic {
			if a.Dynamic {
				return 1
			}
			return -1
		}
		for position, value := range []string{a.Synth, a.FromID, a.ToID, a.Binding} {
			if order := strings.Compare(value, []string{b.Synth, b.FromID, b.ToID, b.Binding}[position]); order != 0 {
				return order
			}
		}
		return 0
	})
	return out
}

// confRank orders the confidence tiers so the best representative wins:
// HIGH(3) > MEDIUM(2) > LOW(1). An empty confidence ranks LOW.
func ConfRank(c Confidence) int {
	switch c {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	default:
		return 1
	}
}

// symbolsFor returns every symbol whose bare or full name matches. Build
// paths precompute the lookup table eagerly (buildSymbolIndex), making
// this O(1); indexes that loaded without it build the table once on the
// first lookup (see below), so no path pays a per-call linear scan.
