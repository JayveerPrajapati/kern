// Package lenses provides named review lenses: weighted priorities over
// evidence types that re-rank claims so a reviewer sees the most relevant
// evidence first. Deterministic, stdlib-only, no I/O.
package lenses

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Lens is a named review lens: a weighted priority over evidence types.
type Lens struct {
	Name       string
	Priorities []EvidencePriority
	Weight     float64 // lens strength for combined lenses (0-1)
}

// EvidencePriority scores one evidence class under a lens.
type EvidencePriority struct {
	Type   domain.EvidenceType
	Weight float64
}

// Registry holds named lenses.
type Registry struct {
	lenses map[string]Lens
}

// NewRegistry creates an empty lens registry.
func NewRegistry() *Registry {
	return &Registry{lenses: map[string]Lens{}}
}

// Register adds a lens under its Name. It errors on an empty name or a
// duplicate registration.
func (r *Registry) Register(l Lens) error {
	if l.Name == "" {
		return fmt.Errorf("lens: empty name")
	}
	if _, ok := r.lenses[l.Name]; ok {
		return fmt.Errorf("lens %q already registered", l.Name)
	}
	r.lenses[l.Name] = l
	return nil
}

// Select returns the lens registered under name, and false when unknown.
func (r *Registry) Select(name string) (Lens, bool) {
	l, ok := r.lenses[name]
	return l, ok
}

// List returns the registered lens names, sorted.
func (r *Registry) List() []string {
	return slices.Sorted(maps.Keys(r.lenses))
}

// SecurityLens prioritizes policy and runtime evidence: what a security
// review needs to see first.
func SecurityLens() Lens {
	return Lens{
		Name: "security",
		Priorities: []EvidencePriority{
			{Type: domain.EvidencePolicy, Weight: 1.0},
			{Type: domain.EvidenceRuntime, Weight: 0.8},
			{Type: domain.EvidenceGraph, Weight: 0.6},
			{Type: domain.EvidenceGit, Weight: 0.5},
			{Type: domain.EvidenceTest, Weight: 0.4},
			{Type: domain.EvidenceBuild, Weight: 0.3},
			{Type: domain.EvidenceMemory, Weight: 0.2},
		},
		Weight: 1.0,
	}
}

// PerformanceLens prioritizes runtime evidence: metrics, traces, and logs.
func PerformanceLens() Lens {
	return Lens{
		Name: "performance",
		Priorities: []EvidencePriority{
			{Type: domain.EvidenceRuntime, Weight: 1.0},
			{Type: domain.EvidenceGraph, Weight: 0.7},
			{Type: domain.EvidenceTest, Weight: 0.5},
			{Type: domain.EvidenceGit, Weight: 0.4},
			{Type: domain.EvidenceBuild, Weight: 0.3},
			{Type: domain.EvidencePolicy, Weight: 0.2},
			{Type: domain.EvidenceMemory, Weight: 0.2},
		},
		Weight: 1.0,
	}
}

// MaintainabilityLens prioritizes structure and test evidence: what a
// maintainability review needs to see first.
func MaintainabilityLens() Lens {
	return Lens{
		Name: "maintainability",
		Priorities: []EvidencePriority{
			{Type: domain.EvidenceGraph, Weight: 1.0},
			{Type: domain.EvidenceTest, Weight: 0.7},
			{Type: domain.EvidenceGit, Weight: 0.6},
			{Type: domain.EvidenceBuild, Weight: 0.4},
			{Type: domain.EvidenceMemory, Weight: 0.4},
			{Type: domain.EvidencePolicy, Weight: 0.2},
			{Type: domain.EvidenceRuntime, Weight: 0.3},
		},
		Weight: 1.0,
	}
}

// ArchitectureLens prioritizes graph and policy evidence: how a change fits
// the system's structure and rules.
func ArchitectureLens() Lens {
	return Lens{
		Name: "architecture",
		Priorities: []EvidencePriority{
			{Type: domain.EvidenceGraph, Weight: 1.0},
			{Type: domain.EvidencePolicy, Weight: 0.6},
			{Type: domain.EvidenceMemory, Weight: 0.5},
			{Type: domain.EvidenceGit, Weight: 0.5},
			{Type: domain.EvidenceTest, Weight: 0.4},
			{Type: domain.EvidenceRuntime, Weight: 0.3},
			{Type: domain.EvidenceBuild, Weight: 0.3},
		},
		Weight: 1.0,
	}
}

// BalancedLens weights every evidence type equally (1.0): it never reorders
// claims (all scores tie, and the sort is stable).
func BalancedLens() Lens {
	types := []domain.EvidenceType{
		domain.EvidenceGraph, domain.EvidenceTest, domain.EvidenceBuild,
		domain.EvidenceGit, domain.EvidenceRuntime, domain.EvidenceMemory,
		domain.EvidencePolicy,
	}
	ps := make([]EvidencePriority, 0, len(types))
	for _, t := range types {
		ps = append(ps, EvidencePriority{Type: t, Weight: 1.0})
	}
	return Lens{Name: "balanced", Priorities: ps, Weight: 1.0}
}

// NewRegistryWithBuiltins returns a registry pre-populated with the five
// built-in lenses: security, performance, maintainability, architecture,
// balanced.
func NewRegistryWithBuiltins() *Registry {
	r := NewRegistry()
	_ = r.Register(SecurityLens())
	_ = r.Register(PerformanceLens())
	_ = r.Register(MaintainabilityLens())
	_ = r.Register(ArchitectureLens())
	_ = r.Register(BalancedLens())
	return r
}

// Resolve parses a lens name into a Lens. A single name selects the built-in
// lens of that name; a combined name joins several lenses with "+" or ","
// (e.g. "security+maintainability" or "security, maintainability"), merged
// via CombinedLens. Unknown names error with the available lens list.
func Resolve(name string) (Lens, error) {
	raw := strings.FieldsFunc(name, func(r rune) bool { return r == '+' || r == ',' })
	parts := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return Lens{}, fmt.Errorf("empty lens name")
	}
	r := NewRegistryWithBuiltins()
	if len(parts) == 1 {
		l, ok := r.Select(parts[0])
		if !ok {
			return Lens{}, fmt.Errorf("unknown lens %q (available: %s)", parts[0], strings.Join(r.List(), ", "))
		}
		return l, nil
	}
	ls := make([]Lens, 0, len(parts))
	for _, p := range parts {
		l, ok := r.Select(p)
		if !ok {
			return Lens{}, fmt.Errorf("unknown lens %q (available: %s)", p, strings.Join(r.List(), ", "))
		}
		ls = append(ls, l)
	}
	return CombinedLens(strings.Join(parts, "+"), ls...), nil
}

// CombinedLens merges several lenses into one preset (e.g. "security-focused"
// = security + balanced): priorities merged per evidence type by AVERAGE
// weight across the input lenses (deterministic; types absent from all inputs
// are omitted); the combined Weight is the average of input Weights.
func CombinedLens(name string, lenses ...Lens) Lens {
	typeWeights := map[domain.EvidenceType][]float64{}
	var totalWeight float64
	for _, l := range lenses {
		totalWeight += l.Weight
		for _, p := range l.Priorities {
			typeWeights[p.Type] = append(typeWeights[p.Type], p.Weight)
		}
	}
	merged := make([]EvidencePriority, 0, len(typeWeights))
	for t, ws := range typeWeights {
		var sum float64
		for _, w := range ws {
			sum += w
		}
		merged = append(merged, EvidencePriority{Type: t, Weight: sum / float64(len(ws))})
	}
	// Deterministic order: sort by evidence type.
	sort.Slice(merged, func(i, j int) bool { return merged[i].Type < merged[j].Type })

	var avgWeight float64
	if len(lenses) > 0 {
		avgWeight = totalWeight / float64(len(lenses))
	}
	return Lens{Name: name, Priorities: merged, Weight: avgWeight}
}

// ApplyLens re-ranks claims by their evidence: score(claim) = sum of lens
// priority weights over claim.Evidence[].Type (types without a priority score
// 0). Stable sort by score DESCENDING — ties keep original order, so the
// balanced lens (all 1.0) never reorders. Never drops claims.
func ApplyLens(l Lens, claims []domain.Claim) []domain.Claim {
	if len(claims) == 0 {
		return claims
	}
	type scoredClaim struct {
		claim domain.Claim
		score float64
	}
	scored := make([]scoredClaim, len(claims))
	for i, c := range claims {
		var s float64
		for _, e := range c.Evidence {
			s += priorityWeight(l, e.Type)
		}
		scored[i] = scoredClaim{claim: c, score: s}
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	out := make([]domain.Claim, len(scored))
	for i, s := range scored {
		out[i] = s.claim
	}
	return out
}

// priorityWeight returns the lens weight for an evidence type, or 0 when the
// lens does not score that type.
func priorityWeight(l Lens, t domain.EvidenceType) float64 {
	for _, p := range l.Priorities {
		if p.Type == t {
			return p.Weight
		}
	}
	return 0
}

// RenderPriorities formats the lens priorities as "type=weight" pairs sorted
// by weight DESC then type ASC, ", "-joined, weights %.2f.
func RenderPriorities(l Lens) string {
	ps := append([]EvidencePriority(nil), l.Priorities...)
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Weight != ps[j].Weight {
			return ps[i].Weight > ps[j].Weight
		}
		return ps[i].Type < ps[j].Type
	})
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		parts = append(parts, fmt.Sprintf("%s=%.2f", p.Type, p.Weight))
	}
	return strings.Join(parts, ", ")
}
