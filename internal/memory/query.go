package memory

import (
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// Query specifies how to retrieve engineering memories.
type Query struct {
	Text            string            // keyword/substring match on content
	Type            domain.MemoryType // filter by type (empty = all)
	Scope           string            // filter by scope (e.g. "service:PaymentService")
	Tags            []string          // filter by tags (any match)
	Entity          string            // retrieve memories about a specific entity (matched against Scope + Tags + Content)
	Service         string            // retrieve memories about a service (matched against Scope starting with "service:")
	Incident        string            // retrieve incident memories (Type=incident, matched against Scope starting with "incident:")
	Subject         string            // filter by subject (empty = all)
	Provenance      string            // filter by provenance (empty = all)
	RelatedEntities []string          // filter by related entities (any match; empty = all)
	Task            string            // retrieve memories about a task (Scope prefix "task:")
	Repository      string            // retrieve memories about a repository (Scope prefix "repository:")
	Architecture    string            // retrieve memories about an architecture topic (Scope prefix "architecture:")
	Global          bool              // if true, match only memories with empty/unscoped Scope
	Organization    string            // match Scope prefix "organization:"
	Module          string            // match Scope prefix "module:"
	File            string            // match Scope prefix "file:"
	Agent           string            // match Scope prefix "agent:"
	Since           time.Time         // if non-zero, only memories with CreatedAt >= Since
	Until           time.Time         // if non-zero, only memories with CreatedAt <= Until
	Limit           int               // max results (0 = unlimited)
}

// Recall returns the memories matching the query, ranked by relevance.
// Filters are applied by field, then remaining memories are scored by
// MatchScore, sorted by score then recency, and truncated to Limit.
func (s *MemoryStore) Recall(query Query) ([]domain.Memory, error) {
	start := time.Now()
	ms := s.load()
	var out []domain.Memory
	for _, m := range ms {
		if query.Type != "" && m.Type != query.Type {
			continue
		}
		if query.Service != "" && !scopeHasPrefix(m.Scope, "service:", query.Service) {
			continue
		}
		if query.Incident != "" && !scopeHasPrefix(m.Scope, "incident:", query.Incident) {
			continue
		}
		if query.Task != "" && !scopeHasPrefix(m.Scope, "task:", query.Task) {
			continue
		}
		if query.Repository != "" && !scopeHasPrefix(m.Scope, "repository:", query.Repository) {
			continue
		}
		if query.Architecture != "" && !scopeHasPrefix(m.Scope, "architecture:", query.Architecture) {
			continue
		}
		if query.Global {
			if m.Scope != "" && !strings.EqualFold(m.Scope, "global") {
				continue
			}
		}
		if query.Organization != "" && !scopeHasPrefix(m.Scope, "organization:", query.Organization) {
			continue
		}
		if query.Module != "" && !scopeHasPrefix(m.Scope, "module:", query.Module) {
			continue
		}
		if query.File != "" && !scopeHasPrefix(m.Scope, "file:", query.File) {
			continue
		}
		if query.Agent != "" && !scopeHasPrefix(m.Scope, "agent:", query.Agent) {
			continue
		}
		if len(query.Tags) > 0 && !tagsOverlap(m.Tags, query.Tags) {
			continue
		}
		if query.Subject != "" && !strings.EqualFold(m.Subject, query.Subject) {
			continue
		}
		if query.Provenance != "" && !strings.EqualFold(m.Provenance, query.Provenance) {
			continue
		}
		if len(query.RelatedEntities) > 0 && !entitiesOverlap(m.RelatedEntities, query.RelatedEntities) {
			continue
		}
		if !query.Since.IsZero() && m.CreatedAt.Before(query.Since) {
			continue
		}
		if !query.Until.IsZero() && m.CreatedAt.After(query.Until) {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := MatchScore(out[i], query), MatchScore(out[j], query)
		if si != sj {
			return si > sj
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if query.Limit > 0 && len(out) > query.Limit {
		out = out[:query.Limit]
	}
	metrics.Default().RecordMemoryRecall(time.Since(start))
	return out, nil
}

// scopeHasPrefix reports whether scope is exactly prefix+id or uses it as a
// path segment delimiter (e.g. "service:PaymentService/submodule").
func scopeHasPrefix(scope, prefix, id string) bool {
	scope = strings.ToLower(scope)
	prefix = strings.ToLower(prefix)
	id = strings.ToLower(id)
	if id == "" {
		return false
	}
	if !strings.HasPrefix(scope, prefix) {
		return false
	}
	rest := scope[len(prefix):]
	return rest == id || strings.HasPrefix(rest, id+"/") || strings.HasPrefix(rest, id+":")
}

// ScopeKind is the category portion of a "scope_type:identifier" scope.
type ScopeKind string

const (
	ScopeGlobal       ScopeKind = "global"
	ScopeOrganization ScopeKind = "organization"
	ScopeRepository   ScopeKind = "repository"
	ScopeService      ScopeKind = "service"
	ScopeModule       ScopeKind = "module"
	ScopeFile         ScopeKind = "file"
	ScopeTask         ScopeKind = "task"
	ScopeIncident     ScopeKind = "incident"
	ScopeAgent        ScopeKind = "agent"
)

// ParseScope splits a scope string like "service:payments" into
// ("service", "payments"). A scope with no colon returns ("", scope).
// An empty scope returns ("global", "").
func ParseScope(scope string) (ScopeKind, string) {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return ScopeGlobal, ""
	}
	idx := strings.IndexByte(scope, ':')
	if idx < 0 {
		return "", scope
	}
	return ScopeKind(scope[:idx]), scope[idx+1:]
}

// tagsOverlap reports whether any memory tag matches any query tag.
func tagsOverlap(memTags, queryTags []string) bool {
	for _, qt := range queryTags {
		for _, mt := range memTags {
			if strings.EqualFold(qt, mt) {
				return true
			}
		}
	}
	return false
}

// entitiesOverlap reports whether any memory related entity matches any query
// related entity.
func entitiesOverlap(memEntities, queryEntities []string) bool {
	for _, qe := range queryEntities {
		for _, me := range memEntities {
			if strings.EqualFold(qe, me) {
				return true
			}
		}
	}
	return false
}

// MatchScore scores how well a memory matches a query (for ranking).
// Higher = better match. Uses keyword overlap on Content plus bonuses for
// type/scope/tag matching and entity matches.
func MatchScore(mem domain.Memory, q Query) int {
	score := 0

	// Keyword overlap on content: base relevance.
	qtoks := tokens(q.Text)
	if len(qtoks) > 0 {
		set := map[string]bool{}
		for _, t := range tokens(mem.Content) {
			set[t] = true
		}
		for _, t := range qtoks {
			if set[t] {
				score += 2
			}
		}
	}

	// Exact type match.
	if q.Type != "" && mem.Type == q.Type {
		score += 4
	}

	// Exact tag matches.
	if len(q.Tags) > 0 {
		for _, mt := range mem.Tags {
			for _, qt := range q.Tags {
				if strings.EqualFold(mt, qt) {
					score += 5
				}
			}
		}
	}

	// Entity match against Scope + Tags + Content.
	if q.Entity != "" {
		needle := strings.ToLower(q.Entity)
		if containsFold(mem.Scope, needle) ||
			tagsContainFold(mem.Tags, needle) ||
			containsFold(mem.Content, needle) {
			score += 6
		}
	}

	// Service / incident scope alignment.
	if q.Service != "" && scopeHasPrefix(mem.Scope, "service:", q.Service) {
		score += 6
	}
	if q.Incident != "" && scopeHasPrefix(mem.Scope, "incident:", q.Incident) {
		score += 6
	}
	if q.Task != "" && scopeHasPrefix(mem.Scope, "task:", q.Task) {
		score += 6
	}
	if q.Repository != "" && scopeHasPrefix(mem.Scope, "repository:", q.Repository) {
		score += 6
	}
	if q.Architecture != "" && scopeHasPrefix(mem.Scope, "architecture:", q.Architecture) {
		score += 6
	}
	if q.Global {
		if mem.Scope == "" || strings.EqualFold(mem.Scope, "global") {
			score += 6
		}
	}
	if q.Organization != "" && scopeHasPrefix(mem.Scope, "organization:", q.Organization) {
		score += 6
	}
	if q.Module != "" && scopeHasPrefix(mem.Scope, "module:", q.Module) {
		score += 6
	}
	if q.File != "" && scopeHasPrefix(mem.Scope, "file:", q.File) {
		score += 6
	}
	if q.Agent != "" && scopeHasPrefix(mem.Scope, "agent:", q.Agent) {
		score += 6
	}

	// Subject match.
	if q.Subject != "" && strings.EqualFold(mem.Subject, q.Subject) {
		score += 6
	}

	// Provenance match.
	if q.Provenance != "" && strings.EqualFold(mem.Provenance, q.Provenance) {
		score += 5
	}

	// Related entity match.
	if len(q.RelatedEntities) > 0 && entitiesOverlap(mem.RelatedEntities, q.RelatedEntities) {
		score += 5
	}

	return score
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), needle)
}

func tagsContainFold(tags []string, needle string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), needle) {
			return true
		}
	}
	return false
}
