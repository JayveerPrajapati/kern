package context

import (
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Phase 15 — Freshness.
//
// This file adds the remaining freshness primitives:
//
//	15.3  invalidation marker          NewInvalidation / InvalidationMarker
//	15.4  memory supersession          (implemented in internal/memory)
//	15.5  freshness in scoring         ScoreEvidenceFreshness / RiskFreshnessMultiplier
//
// All are deterministic.

// defaultFreshBound is the default staleness bound used by the freshness
// scorers when no bound is supplied.
const defaultFreshBound = 7 * 24 * time.Hour

// InvalidationMarker is a record that a piece of context/memory was invalidated
// (Phase 15.3). It lets consumers know when a previously-trusted fact no longer
// holds, so stale context is not silently reused. It is deterministic and
// timestamped.
type InvalidationMarker struct {
	// Entity is the id/scope of the invalidated item (e.g. a memory id, a
	// symbol, a service).
	Entity string `json:"entity"`
	// Reason is why the item was invalidated.
	Reason string `json:"reason"`
	// Source is what invalidated it (e.g. "file-watch", "user", "superseded").
	Source string `json:"source"`
	// At is when the invalidation occurred.
	At time.Time `json:"at"`
}

// NewInvalidationMarker builds an invalidation marker at the given time.
func NewInvalidationMarker(entity, reason, source string, at time.Time) InvalidationMarker {
	return InvalidationMarker{Entity: entity, Reason: reason, Source: source, At: at}
}

// FreshnessClass is the freshness classification of an item (Phase 15.5).
type FreshnessClass string

const (
	FreshnessFresh FreshnessClass = "FRESH"
	FreshnessAging FreshnessClass = "AGING"
	FreshnessStale FreshnessClass = "STALE"
)

// evidenceFreshnessClass classifies the age of an evidence timestamp against a
// bound.
func evidenceFreshnessClass(ts time.Time, now time.Time, bound time.Duration) FreshnessClass {
	if bound <= 0 {
		bound = defaultFreshBound
	}
	if ts.IsZero() {
		return FreshnessFresh // unknown age: not penalized
	}
	age := now.Sub(ts)
	switch {
	case age >= bound:
		return FreshnessStale
	case age >= bound/2:
		return FreshnessAging
	default:
		return FreshnessFresh
	}
}

// evidenceScore returns a 0.0-1.0 freshness score for evidence: 1.0 fresh,
// 0.5 aging, 0.1 stale (so stale evidence is heavily discounted in any
// aggregate evidence score).
func evidenceScore(ts time.Time, now time.Time, bound time.Duration) float64 {
	switch evidenceFreshnessClass(ts, now, bound) {
	case FreshnessFresh:
		return 1.0
	case FreshnessAging:
		return 0.5
	default:
		return 0.1
	}
}

// EvidenceFreshness reports the freshness class and score of an evidence item
// (Phase 15.5). A nil bound uses the default staleness bound.
func EvidenceFreshness(ev domain.Evidence, now time.Time, bound time.Duration) (FreshnessClass, float64) {
	cls := evidenceFreshnessClass(ev.Timestamp, now, bound)
	return cls, evidenceScore(ev.Timestamp, now, bound)
}

// RiskFreshnessMultiplier scales a risk score by the freshness of its evidence
// (Phase 15.5): a risk supported by fresh evidence keeps full weight; a risk
// supported only by stale evidence is scaled down, because stale signals are
// less reliable. It returns the multiplier in [0, 1] and the classification.
func RiskFreshnessMultiplier(evidence []domain.Evidence, now time.Time, bound time.Duration) (float64, FreshnessClass) {
	if len(evidence) == 0 {
		return 1.0, FreshnessFresh // no evidence: do not penalize the risk
	}
	worst := FreshnessFresh
	for _, ev := range evidence {
		cls := evidenceFreshnessClass(ev.Timestamp, now, bound)
		if cls == FreshnessStale {
			return 0.5, FreshnessStale
		}
		if cls == FreshnessAging {
			worst = FreshnessAging
		}
	}
	if worst == FreshnessAging {
		return 0.8, FreshnessAging
	}
	return 1.0, FreshnessFresh
}

// FreshnessAdjustedConfidence scales a claim/evidence confidence by the
// freshness of the evidence set (Phase 15.5). It reuses the worst-class
// freshness classification over the evidence and applies a deterministic
// multiplier: 1.0 for fresh evidence, 0.8 for aging, 0.5 for stale. A claim
// with no evidence returns its confidence unchanged (nothing to penalize).
func FreshnessAdjustedConfidence(conf float64, evidence []domain.Evidence, now time.Time, bound time.Duration) float64 {
	if len(evidence) == 0 {
		return conf
	}
	m, _ := RiskFreshnessMultiplier(evidence, now, bound)
	return conf * m
}

// FreshnessAdjustedRisk scales a risk score by the freshness of the evidence
// backing it (Phase 15.5). It reuses RiskFreshnessMultiplier so a risk
// supported only by stale evidence is down-weighted (score = score * multiplier)
// and a "freshness:<class>" factor is appended when the multiplier is < 1. The
// risk Level and block/approval state are preserved; only the Score changes.
func FreshnessAdjustedRisk(risk domain.Risk, evidence []domain.Evidence, now time.Time, bound time.Duration) domain.Risk {
	m, cls := RiskFreshnessMultiplier(evidence, now, bound)
	if m < 1.0 {
		risk.Score = risk.Score * m
		risk.Factors = append(risk.Factors, "freshness:"+strings.ToLower(string(cls)))
	}
	return risk
}

// ToolFreshnessBoost returns a deterministic 0..1 multiplier a capability/tool
// planner can use to down-rank tools backed by stale evidence (Phase 15.5). A
// tool with no evidence (or fresh evidence) keeps full weight (1.0); stale
// evidence yields a value < 1.0. The tool string is accepted for signature
// completeness; the boost is derived entirely from the evidence freshness.
func ToolFreshnessBoost(tool string, evidence []domain.Evidence, now time.Time, bound time.Duration) float64 {
	if len(evidence) == 0 {
		return 1.0
	}
	m, _ := RiskFreshnessMultiplier(evidence, now, bound)
	return m
}

// InvalidateContext records invalidation markers for a set of stale entities
// (Phase 15.3). It is a convenience that fans an invalidation out to a list of
// entities with the same reason/source/at.
func InvalidateContext(entities []string, reason, source string, at time.Time) []InvalidationMarker {
	markers := make([]InvalidationMarker, 0, len(entities))
	for _, e := range entities {
		markers = append(markers, NewInvalidationMarker(e, reason, source, at))
	}
	return markers
}