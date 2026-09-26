// Architecture-drift extraction (Self-Improvement Tier 3 #9): learns from the
// observed ARCHITECTURE.md ledger drift log which change classes break the
// gates and proposes pre-flag RECOMMENDATION claims — memory only.
package learning

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ArchitectureDriftPatterns groups drift records by (subsystem, violation
// kind); each group with >= threshold records becomes one RECOMMENDATION
// pattern: "changes touching subsystem <sub> are prone to <kind> violations
// (N recorded) — pre-flag at plan time", scoped "drift:<sub>:<kind>", with
// provenance = deduped sorted change refs (or subsystem/kind labels) plus the
// newest observation time. Below-threshold groups contribute nothing.
// Deterministic: sorted groups, deduped+sorted sources, patterns ordered by
// scope then statement. threshold <= 0 is treated as 1.
func ArchitectureDriftPatterns(records []domain.ArchitectureDriftRecord, threshold int) []Pattern {
	if threshold <= 0 {
		threshold = 1
	}
	groups := map[string][]domain.ArchitectureDriftRecord{}
	for _, r := range records {
		sub := strings.TrimSpace(r.Subsystem)
		kind := strings.TrimSpace(r.ViolationKind)
		if sub == "" || kind == "" {
			continue
		}
		groups["drift:"+sub+":"+kind] = append(groups["drift:"+sub+":"+kind], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	patterns := make([]Pattern, 0, len(keys))
	for _, key := range keys {
		g := groups[key]
		if len(g) >= threshold {
			patterns = append(patterns, driftPattern(key, g))
		}
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Key != patterns[j].Key {
			return patterns[i].Key < patterns[j].Key
		}
		return patterns[i].Statement < patterns[j].Statement
	})
	return patterns
}

// driftPattern assembles the RECOMMENDATION pattern for one (sub, kind) group.
func driftPattern(key string, records []domain.ArchitectureDriftRecord) Pattern {
	n := len(records)
	sub := records[0].Subsystem
	kind := records[0].ViolationKind
	statement := fmt.Sprintf("changes touching subsystem %s are prone to %s violations (%d recorded) — pre-flag at plan time", sub, kind, n)
	srcSet := map[string]bool{}
	var latest time.Time
	for _, r := range records {
		src := strings.TrimSpace(r.Change)
		if src == "" {
			src = fmt.Sprintf("drift %s (%s)", sub, kind)
		}
		srcSet[src] = true
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
		Key:        key,
		Count:      n,
		Scopes:     []string{key},
		Sample:     []string{statement},
		Created:    latest,
		ClaimType:  domain.ClaimRecommendation,
		Provenance: ClaimProvenance{Sources: srcs, Count: n, Latest: latest},
		Statement:  statement,
	}
}
