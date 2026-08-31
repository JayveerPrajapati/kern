// Package consistency provides a cross-engine, multi-source consistency checker
// ( ). It compares several knowledge sources — graph,
// digital twin, memory, Git, runtime, architecture, tests — about the same
// subject, detects version/fingerprint mismatches as stale, and produces
// human-readable conflict explanations. All logic is deterministic: no live LLM.
package consistency

import (
	"context"
	"time"

	contextpkg "github.com/JayveerPrajapati/kern/internal/context"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Source is a knowledge source that can be fingerprinted and checked for its
// claim about a subject. Every engine compares the version each source reports
// and the claims they make, so the result is deterministic and explainable.
type Source interface {
	// Version is a fingerprint/version of the source's current content. Two
	// sources with different versions for the same subject are treated as
	// potentially inconsistent; the older one is invalidated as stale.
	Version() string
	// UpdatedAt is when the source's content was last refreshed, used for
	// staleness checks against a freshness bound.
	UpdatedAt() time.Time
	// Claim returns the source's claim about the subject, and whether it has
	// one. ok is false when the source is silent about the subject.
	Claim(subject string) (value string, ok bool)
	// Name identifies the source (GRAPH, TWIN, MEMORY, GIT, RUNTIME,
	// ARCHITECTURE, TESTS).
	Name() domain.KnowledgeSource
}

// Engine runs a consistency check across a set of sources. It is stateless and
// safe for concurrent use.
type Engine struct{}

// NewEngine builds a consistency-checking engine.
func NewEngine() *Engine { return &Engine{} }

// Result is the outcome of checking a set of subjects against a set of
// sources: the overall classification, a full domain report, per-subject
// classifications, and the freshness invalidation markers emitted for stale
// sources.
type Result struct {
	// Overall is the highest-severity classification across all subjects
	// (one of domain.Conflict*).
	Overall domain.ConflictResult
	// Report carries conflicts, stale subjects, and confidence downgrades in
	// the canonical domain shape.
	Report domain.ConsistencyReport
	// PerSubject maps each subject to its own classification.
	PerSubject map[string]domain.ConflictResult
	// Invalidations are the freshness markers recorded for every stale
	// source/subject, so stale context is not silently reused.
	Invalidations []contextpkg.InvalidationMarker
}

// Check checks every subject against sources, producing a per-subject and
// overall classification.
// Stale detection: a source is STALE for a subject when its version differs
// from the newest version reported by any source claiming that subject, or
// when its UpdatedAt is older than the freshness bound. Stale sources are
// invalidated and excluded from conflict resolution.
// Conflict detection: among the non-stale sources claiming a subject, if any
// two claim distinct values the subject is in CONFLICT and the first distinct
// pair is reported with a full explanation.
// UNKNOWN: a subject no source claims. NO_CONFLICT: every claimed subject
// agrees across fresh sources. The overall classification is the highest
// severity: CONFLICT > STALE > UNKNOWN > NO_CONFLICT.
func (e *Engine) Check(ctx context.Context, sources []Source, subjects []string, now time.Time, bound time.Duration) Result {
	res := Result{PerSubject: map[string]domain.ConflictResult{}}
	overall := domain.ConflictNone

	for _, sub := range subjects {
		cls, conflicts, stale, downgrades := e.classifySubject(sources, sub, now, bound)
		res.PerSubject[sub] = cls
		res.Report.Conflicts = append(res.Report.Conflicts, conflicts...)
		res.Report.StaleSubjects = append(res.Report.StaleSubjects, stale...)
		if len(downgrades) > 0 {
			if res.Report.ConfidenceDowngrades == nil {
				res.Report.ConfidenceDowngrades = map[string]float64{}
			}
			for k, v := range downgrades {
				res.Report.ConfidenceDowngrades[k] = v
			}
		}
		res.Invalidations = append(res.Invalidations, contextpkg.InvalidateContext(
			stale, "version mismatch supersedes older claim", "consistency", now)...)
		overall = highest(overall, cls)
	}

	res.Overall = overall
	res.Report.Result = overall
	return res
}

// sourceClaim is one source's evidence about a subject.
type sourceClaim struct {
	value     string
	version   string
	updatedAt time.Time
	name      domain.KnowledgeSource
}

// classifySubject classifies a single subject across the sources. It returns
// the subject's conflict classification, any conflicts discovered, the stale
// source names, and the confidence downgrades applied.
func (e *Engine) classifySubject(sources []Source, subject string, now time.Time, bound time.Duration) (
	domain.ConflictResult, []domain.ConsistencyConflict, []string, map[string]float64) {

	claims := map[domain.KnowledgeSource]sourceClaim{}
	var order []domain.KnowledgeSource // stable iteration order
	for _, s := range sources {
		v, ok := s.Claim(subject)
		if !ok {
			continue
		}
		if _, seen := claims[s.Name()]; !seen {
			order = append(order, s.Name())
		}
		claims[s.Name()] = sourceClaim{value: v, version: s.Version(), updatedAt: s.UpdatedAt(), name: s.Name()}
	}

	if len(claims) == 0 {
		return domain.ConflictUnknown, nil, nil, nil
	}

	// newest = the source with the most recent UpdatedAt among those claiming.
	var newestName domain.KnowledgeSource
	var newestAt time.Time
	for _, n := range order {
		if claims[n].updatedAt.After(newestAt) {
			newestAt, newestName = claims[n].updatedAt, n
		}
	}
	refVersion := claims[newestName].version

	var conflicts []domain.ConsistencyConflict
	var stale []string
	downgrades := map[string]float64{}
	valid := map[domain.KnowledgeSource]string{}

	for _, n := range order {
		c := claims[n]
		isStale := c.version != refVersion || (bound > 0 && now.Sub(c.updatedAt) >= bound)
		if isStale {
			stale = append(stale, string(n))
			downgrades[string(n)] = 0.5
			continue
		}
		valid[n] = c.value
	}

	if len(valid) == 0 {
		// Every source claiming the subject is stale.
		return domain.ConflictStale, conflicts, stale, downgrades
	}

	// Determine agreement among fresh sources in stable order.
	var freshOrder []domain.KnowledgeSource
	for _, n := range order {
		if _, ok := valid[n]; ok {
			freshOrder = append(freshOrder, n)
		}
	}

	first := valid[freshOrder[0]]
	agree := true
	for _, n := range freshOrder[1:] {
		if valid[n] != first {
			agree = false
			break
		}
	}
	if !agree {
		// Report the first distinct pair in stable order.
	outer:
		for i := 0; i < len(freshOrder); i++ {
			for j := i + 1; j < len(freshOrder); j++ {
				a, b := freshOrder[i], freshOrder[j]
				if valid[a] != valid[b] {
					conflicts = append(conflicts, domain.ConsistencyConflict{
						Subject:     subject,
						ClaimA:      valid[a],
						SourceA:     a,
						VersionA:    claims[a].version,
						ClaimB:      valid[b],
						SourceB:     b,
						VersionB:    claims[b].version,
						SourceNewer: newerOf(claims[a].updatedAt, claims[b].updatedAt, a, b),
					})
					break outer
				}
			}
		}
		return domain.ConflictPresent, conflicts, stale, downgrades
	}

	// Fresh sources agree, but if any source claiming this subject was stale
	// the evidence cannot be fully trusted → downgrade the subject to STALE
	if len(stale) > 0 {
		return domain.ConflictStale, conflicts, stale, downgrades
	}
	return domain.ConflictNone, conflicts, stale, downgrades
}

// newerOf returns the source (a or b) with the later UpdatedAt.
func newerOf(ua, ub time.Time, a, b domain.KnowledgeSource) domain.KnowledgeSource {
	if ua.Before(ub) {
		return b
	}
	return a
}

// highest returns the higher-severity classification, with priority
// CONFLICT > STALE > UNKNOWN > NO_CONFLICT.
func highest(a, b domain.ConflictResult) domain.ConflictResult {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func rank(r domain.ConflictResult) int {
	switch r {
	case domain.ConflictPresent:
		return 4
	case domain.ConflictStale:
		return 3
	case domain.ConflictUnknown:
		return 2
	default:
		return 1
	}
}
