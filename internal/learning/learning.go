// Package learning extracts recurring patterns from persisted engineering
// memory and surfaces them as recallable memory. Extraction is deterministic,
// runs locally, and uses only the standard library.
package learning

import (
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/learnclaim"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// Pattern is a recurring change→outcome pattern detected across persisted
// engineering memory.
type Pattern struct {
	Key            string    // normalized pattern key ("scope:..." or "sig:...")
	Count          int       // number of memories sharing Key
	Scopes         []string  // distinct scopes among the grouped memories
	Sample         []string  // most recent contents (cap 3)
	Created        time.Time // newest memory's CreatedAt
	Incidents      []string  // incident IDs that match this pattern (empty when none known)
	Recommendation string    // actionable advice derived from the pattern (empty when none)
	// ClaimType is the typed-claim class of the grouped memories; empty for
	// plain-text groups (typed-claim memories key separately, see keyFor).
	ClaimType domain.ClaimType
	// Provenance summarizes where the grouped memories came from
	// (deterministic: Sources sorted, Count, Latest newest timestamp).
	Provenance ClaimProvenance
	// Statement is an optional explicit claim statement (e.g. a calibration
	// verdict); when set, patternContent writes it verbatim, else falls back
	// to Recommendation.
	Statement string
}

// ClaimProvenance is a compatibility alias for learnclaim.ClaimProvenance:
// the type moved to internal/learnclaim in the claim-rendering split, and
// callers such as internal/app keep referencing learning.ClaimProvenance.
type ClaimProvenance = learnclaim.ClaimProvenance

// Extractor derives patterns from a typed memory store.
type Extractor struct {
	mem *memory.MemoryStore
}

// New returns an Extractor backed by the given memory store.
func New(mem *memory.MemoryStore) *Extractor {
	return &Extractor{mem: mem}
}

// Patterns reads ALL memories and groups them by a deterministic key:
// - "scope:"+Scope when the memory has a non-empty Scope, otherwise
// - "sig:"+hash12 where hash12 is the first 12 characters of a content
// hash computed from the trimmed content.
// Each group becomes a Pattern: Count is the number of grouped memories,
// Scopes the distinct scopes (sorted), Sample the most recent contents capped
// at 3 (sorted by CreatedAt desc, then Content asc), Created the newest
// memory's timestamp, and Incidents the sorted incident IDs (memory Subject)
// of any incident-derived memories in the group. Results are sorted by Count
// desc, then Key asc.
func (e *Extractor) Patterns() ([]Pattern, error) {
	ms, err := e.mem.List("")
	if err != nil {
		return nil, err
	}
	groups := map[string][]domain.Memory{}
	for _, m := range ms {
		key := keyFor(m)
		groups[key] = append(groups[key], m)
	}
	patterns := make([]Pattern, 0, len(groups))
	for key, group := range groups {
		patterns = append(patterns, buildPattern(key, group))
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Count != patterns[j].Count {
			return patterns[i].Count > patterns[j].Count
		}
		return patterns[i].Key < patterns[j].Key
	})
	return patterns, nil
}

// Surface returns only the patterns with Count >= threshold (auto-surfacing).
// A threshold <= 0 is treated as 1, so Surface returns everything. It is
// read-only and never writes to the store.
func (e *Extractor) Surface(threshold int) ([]Pattern, error) {
	if threshold <= 0 {
		threshold = 1
	}
	patterns, err := e.Patterns()
	if err != nil {
		return nil, err
	}
	out := make([]Pattern, 0, len(patterns))
	for _, p := range patterns {
		if p.Count >= threshold {
			out = append(out, p)
		}
	}
	return out, nil
}

// Remember writes a synthesized MemoryConstraint describing the pattern back
// to the store, preserving the pattern's claim type (Memory.ClaimType) and
// provenance summary (Memory.Provenance) when present. It upserts by scope:
// an existing constraint with the same scope has its content and claim
// metadata replaced in place rather than appending a new entry. keyFor groups
// memories by scope, so naively appending here would write the new constraint
// back into the same pattern group, raising its count forever — the
// self-reinforcing feedback that otherwise grows memory without bound.
func (e *Extractor) Remember(p Pattern) (domain.Memory, error) {
	m := domain.Memory{
		Type:       domain.MemoryConstraint,
		Content:    learnclaim.RenderContent(claimView(p)),
		Source:     "learning",
		Scope:      p.Key,
		Tags:       []string{"pattern", "auto-surfaced"},
		ClaimType:  p.ClaimType,
		Provenance: learnclaim.RenderProvenance(claimView(p)),
	}
	mems, err := e.mem.List(domain.MemoryConstraint)
	if err != nil {
		return domain.Memory{}, err
	}
	for _, existing := range mems {
		if existing.Scope == m.Scope {
			return e.mem.Replace(existing.ID, m)
		}
	}
	return e.mem.Add(m)
}

// keyFor returns the deterministic grouping key for a memory. Memories with a
// typed claim (non-empty ClaimType) group separately from plain-text memories
// under "claim:<TYPE>:"-prefixed keys.
func keyFor(m domain.Memory) string {
	prefix := ""
	if m.ClaimType != "" {
		prefix = "claim:" + string(m.ClaimType) + ":"
	}
	if s := strings.TrimSpace(m.Scope); s != "" {
		return prefix + "scope:" + s
	}
	return prefix + "sig:" + contentHash12(m.Content)
}

// contentHash12 returns the first 12 characters of the content hash of the
// trimmed content.
func contentHash12(content string) string {
	return cache.Hash([]byte(strings.TrimSpace(content)))[:12]
}

// buildPattern derives a Pattern from a group of memories sharing a key.
func buildPattern(key string, group []domain.Memory) Pattern {
	scopeSet := map[string]bool{}
	incidentSet := map[string]bool{}
	sourceSet := map[string]bool{}
	var claimType domain.ClaimType
	var latest time.Time
	for _, m := range group {
		if s := strings.TrimSpace(m.Scope); s != "" {
			scopeSet[s] = true
		}
		// Incident-derived memories (Type MemoryIncident) carry the incident
		// ID in Subject (internal/incident writes Subject: inc.ID); collect
		// them so the pattern records which incidents form this recurring
		// failure class. Deterministic: sorted + deduped below.
		if m.Type == domain.MemoryIncident && strings.TrimSpace(m.Subject) != "" {
			incidentSet[m.Subject] = true
		}
		// Provenance: distinct sources. All members of a group share the same
		// claim type by key construction; take the first non-empty one.
		if m.ClaimType != "" && claimType == "" {
			claimType = m.ClaimType
		}
		if src := strings.TrimSpace(m.Source); src != "" {
			sourceSet[src] = true
		}
		if m.CreatedAt.After(latest) {
			latest = m.CreatedAt
		}
	}
	scopes := make([]string, 0, len(scopeSet))
	for s := range scopeSet {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)

	incidents := make([]string, 0, len(incidentSet))
	for id := range incidentSet {
		incidents = append(incidents, id)
	}
	sort.Strings(incidents)

	sources := make([]string, 0, len(sourceSet))
	for s := range sourceSet {
		sources = append(sources, s)
	}
	sort.Strings(sources)

	// Deterministic: newest first (CreatedAt desc), then Content asc.
	sorted := append([]domain.Memory(nil), group...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].CreatedAt.Equal(sorted[j].CreatedAt) {
			return sorted[i].CreatedAt.After(sorted[j].CreatedAt)
		}
		return sorted[i].Content < sorted[j].Content
	})
	const sampleCap = 3
	samples := make([]string, 0, sampleCap)
	for i, m := range sorted {
		if i >= sampleCap {
			break
		}
		samples = append(samples, m.Content)
	}
	// Best-effort recommendation derived from the sample. If no sample is
	// available there is nothing actionable to say, so leave it empty and let
	// callers fill it in.
	var rec string
	if len(samples) > 0 {
		rec = "Recurring pattern: " + samples[0] + ". Consider documenting this as a constraint."
	}
	return Pattern{
		Key:            key,
		Count:          len(group),
		Scopes:         scopes,
		Sample:         samples,
		Created:        latest,
		Incidents:      incidents,
		Recommendation: rec,
		ClaimType:      claimType,
		Provenance:     ClaimProvenance{Sources: sources, Count: len(group), Latest: latest},
	}
}

// claimView projects a Pattern onto the minimal view the claim renderers
// need, keeping the renderers (internal/learnclaim) independent of the
// learning package.
func claimView(p Pattern) learnclaim.PatternView {
	return learnclaim.PatternView{
		Key:            p.Key,
		Count:          p.Count,
		Scopes:         p.Scopes,
		ClaimType:      p.ClaimType,
		Incidents:      p.Incidents,
		Statement:      p.Statement,
		Recommendation: p.Recommendation,
		Provenance:     p.Provenance,
	}
}
