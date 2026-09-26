// Context-usage extraction (Self-Improvement use-cases Tier 2 #5): learns
// from the observed outcome log which context-packet slices actually got USED
// by task outcomes, and proposes that learning as typed-claim memories
// (RECOMMENDATION for slices never used across N tasks — "shrink or omit",
// INFERENCE for slices used in fewer than half the tasks — "review the
// budget"). The guardrail is "learning proposes, budget approves": the output
// is memory only — nothing here touches internal/context, internal/budget,
// internal/optimize, or packet assembly.

package learning

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// usageGroup accumulates the observed usage records for one context-packet
// slice kind.
type usageGroup struct {
	records []domain.ContextUsageRecord
}

// ContextUsagePatterns derives context-usage signals from observed outcome
// records. Records are grouped by slice kind ("files", "symbols", "memory",
// "incidents", "runtime_evidence", "architecture_rules", ...). Each kind with
// at least threshold records is scored independently:
//
//   - used across NO records → RECOMMENDATION "context slice <slice> unused
//     in N/N tasks — shrink or omit from future packets" (Count = N);
//   - used in fewer than half the records (strictly < 50% of members) →
//     INFERENCE "context slice <slice> used in X of N tasks — review whether
//     its budget is justified";
//   - used in 50%+ of members → nothing (a fully used slice is healthy).
//
// Kinds with zero total members contribute nothing (nothing to learn).
// Deterministic: kinds are sorted, provenance sources are deduped + sorted,
// and the returned patterns are ordered by scope then statement. threshold
// <= 0 is treated as 1.
func ContextUsagePatterns(records []domain.ContextUsageRecord, threshold int) []Pattern {
	if threshold <= 0 {
		threshold = 1
	}
	groups := map[string]*usageGroup{}
	for _, r := range records {
		slice := strings.TrimSpace(r.Slice)
		if slice == "" {
			continue
		}
		g, ok := groups[slice]
		if !ok {
			g = &usageGroup{}
			groups[slice] = g
		}
		g.records = append(g.records, r)
	}

	kinds := make([]string, 0, len(groups))
	for k := range groups {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	patterns := make([]Pattern, 0, len(kinds))
	for _, slice := range kinds {
		g := groups[slice]
		if len(g.records) < threshold {
			continue
		}
		var used, members int
		usedTasks := 0
		for _, r := range g.records {
			used += r.Used
			members += r.Members
			if r.Used > 0 {
				usedTasks++
			}
		}
		if members == 0 {
			continue // nothing to learn from zero-member slices
		}
		var p Pattern
		switch {
		case used == 0:
			p = unusedSlicePattern(slice, g)
		case used*2 < members: // strictly under 50% of members used
			p = partialUsePattern(slice, g, usedTasks)
		default:
			continue // fully (or mostly) used: healthy, no signal
		}
		patterns = append(patterns, p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Key != patterns[j].Key {
			return patterns[i].Key < patterns[j].Key
		}
		return patterns[i].Statement < patterns[j].Statement
	})
	return patterns
}

// unusedSlicePattern assembles the RECOMMENDATION pattern for a slice with
// zero usage across every observed task: propose shrinking or omitting the
// slice from future packets. A future human-approved budget change may act on
// it — nothing here changes packet assembly.
func unusedSlicePattern(slice string, g *usageGroup) Pattern {
	n := len(g.records)
	statement := fmt.Sprintf(
		"context slice %s unused in %d/%d tasks — shrink or omit from future packets",
		slice, n, n)
	return usagePattern("context:"+slice, g, statement, n, domain.ClaimRecommendation)
}

// partialUsePattern assembles the INFERENCE pattern for a slice used in
// strictly fewer than half the observed tasks: recommend reviewing whether the
// slice's budget is justified.
func partialUsePattern(slice string, g *usageGroup, usedTasks int) Pattern {
	n := len(g.records)
	statement := fmt.Sprintf(
		"context slice %s used in %d of %d tasks — review whether its budget is justified",
		slice, usedTasks, n)
	return usagePattern("context:"+slice, g, statement, n, domain.ClaimInference)
}

// usagePattern builds the common Pattern shape: the deterministic
// "context:<slice>" key as scope, the statement as the sample/content source,
// and provenance = the contributing task IDs (deduped + sorted) with the
// newest observed timestamp.
func usagePattern(key string, g *usageGroup, statement string, count int, ct domain.ClaimType) Pattern {
	srcSet := map[string]bool{}
	var latest time.Time
	for _, r := range g.records {
		srcSet["task "+r.Task+" ("+r.Outcome+")"] = true
		if r.At.After(latest) {
			latest = r.At
		}
	}
	srcs := make([]string, 0, len(srcSet))
	for s := range srcSet {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	return Pattern{
		Key:       key,
		Count:     count,
		Scopes:    []string{key},
		Sample:    []string{statement},
		Created:   latest,
		ClaimType: ct,
		Provenance: ClaimProvenance{
			Sources: srcs,
			Count:   count,
			Latest:  latest,
		},
		Statement: statement,
	}
}
