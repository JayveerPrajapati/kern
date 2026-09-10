package lenses

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func statements(claims []domain.Claim) []string {
	out := make([]string, len(claims))
	for i, c := range claims {
		out[i] = c.Statement
	}
	return out
}

func TestRegistryRegisterSelectList(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(SecurityLens()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// duplicate -> error
	if err := r.Register(SecurityLens()); err == nil {
		t.Error("duplicate registration should error")
	}
	// empty name -> error
	if err := r.Register(Lens{Name: ""}); err == nil {
		t.Error("empty-name registration should error")
	}
	// select ok
	l, ok := r.Select("security")
	if !ok || l.Name != "security" {
		t.Errorf("Select(security) = %+v, %v; want ok with name security", l, ok)
	}
	// unknown select -> false
	if _, ok := r.Select("nope"); ok {
		t.Error("Select(unknown) should be false")
	}
	// List sorted
	if err := r.Register(Lens{Name: "zeta"}); err != nil {
		t.Fatalf("Register zeta: %v", err)
	}
	if err := r.Register(Lens{Name: "alpha"}); err != nil {
		t.Fatalf("Register alpha: %v", err)
	}
	got := r.List()
	want := []string{"alpha", "security", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("List() = %v, want %v", got, want)
		}
	}
}

func TestBuiltinLenses(t *testing.T) {
	builtins := []Lens{SecurityLens(), PerformanceLens(), MaintainabilityLens(), ArchitectureLens(), BalancedLens()}
	for _, l := range builtins {
		if l.Name == "" {
			t.Error("built-in lens has empty name")
		}
		if len(l.Priorities) == 0 {
			t.Errorf("%s: empty priorities", l.Name)
		}
		if l.Weight < 0 || l.Weight > 1 {
			t.Errorf("%s: Weight %v outside [0,1]", l.Name, l.Weight)
		}
		for _, p := range l.Priorities {
			if p.Weight < 0 || p.Weight > 1 {
				t.Errorf("%s: priority %s weight %v outside [0,1]", l.Name, p.Type, p.Weight)
			}
		}
	}

	r := NewRegistryWithBuiltins()
	names := r.List()
	want := []string{"architecture", "balanced", "maintainability", "performance", "security"}
	if len(names) != len(want) {
		t.Fatalf("builtin registry List() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("builtin registry List() = %v, want %v", names, want)
		}
	}
}

func TestCombinedLens(t *testing.T) {
	combined := CombinedLens("security-focused", SecurityLens(), BalancedLens())
	if combined.Name != "security-focused" {
		t.Errorf("Name = %q, want security-focused", combined.Name)
	}
	weights := map[domain.EvidenceType]float64{}
	for _, p := range combined.Priorities {
		weights[p.Type] = p.Weight
	}
	if w := weights[domain.EvidencePolicy]; w < 0.99 || w > 1.01 {
		t.Errorf("policy weight = %v, want ~1.0 (avg of 1.0+1.0)", w)
	}
	if w := weights[domain.EvidenceGraph]; w < 0.79 || w > 0.81 {
		t.Errorf("graph weight = %v, want ~0.8 (avg of 0.6+1.0)", w)
	}
	if w := combined.Weight; w < 0.99 || w > 1.01 {
		t.Errorf("combined Weight = %v, want ~1.0 (avg of input weights)", w)
	}

	// Types absent from ALL inputs are omitted; types in one input survive.
	partial := CombinedLens("partial", Lens{Name: "a", Priorities: []EvidencePriority{{Type: domain.EvidenceGraph, Weight: 1.0}}})
	foundGraph := false
	for _, p := range partial.Priorities {
		if p.Type == domain.EvidencePolicy {
			t.Error("policy should be omitted when no input defines it")
		}
		if p.Type == domain.EvidenceGraph {
			foundGraph = true
		}
	}
	if !foundGraph {
		t.Error("graph should be present (defined by one input)")
	}
}

func TestApplyLensSecurity(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "graph-only", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
		{Statement: "test-backed", Evidence: []domain.Evidence{{Type: domain.EvidenceTest}}},
		{Statement: "policy-backed", Evidence: []domain.Evidence{{Type: domain.EvidencePolicy}}},
	}
	ranked := ApplyLens(SecurityLens(), claims)
	// policy 1.0 > graph 0.6 > test 0.4
	want := []string{"policy-backed", "graph-only", "test-backed"}
	got := statements(ranked)
	if len(got) != len(want) {
		t.Fatalf("ApplyLens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ApplyLens = %v, want %v", got, want)
		}
	}

	// Stable order for equal scores: graph ties keep original order.
	eq := ApplyLens(SecurityLens(), []domain.Claim{
		{Statement: "graph-a", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
		{Statement: "graph-b", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
	})
	if eq[0].Statement != "graph-a" || eq[1].Statement != "graph-b" {
		t.Errorf("equal scores should keep original order, got %v", statements(eq))
	}
}

func TestApplyLensBalancedNoReorder(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "a", Evidence: []domain.Evidence{{Type: domain.EvidenceTest}}},
		{Statement: "b", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
		{Statement: "c", Evidence: []domain.Evidence{{Type: domain.EvidencePolicy}}},
		{Statement: "d", Evidence: []domain.Evidence{{Type: domain.EvidenceRuntime}}},
	}
	got := ApplyLens(BalancedLens(), claims)
	for i, c := range claims {
		if got[i].Statement != c.Statement {
			t.Errorf("balanced lens must not reorder: got %v, want %v", statements(got), statements(claims))
		}
	}
}

func TestApplyLensUnknownTypesScoreZero(t *testing.T) {
	claims := []domain.Claim{
		{Statement: "known", Evidence: []domain.Evidence{{Type: domain.EvidencePolicy}}},
		{Statement: "unknown", Evidence: []domain.Evidence{{Type: domain.EvidenceType("quantum")}}},
		{Statement: "no-evidence"},
	}
	got := ApplyLens(SecurityLens(), claims)
	if got[0].Statement != "known" {
		t.Errorf("known-evidence claim should rank first, got %v", statements(got))
	}
	if got[1].Statement != "unknown" || got[2].Statement != "no-evidence" {
		t.Errorf("zero-scoring claims should sink to the bottom, got %v", statements(got))
	}
}

func TestRenderPriorities(t *testing.T) {
	sec := RenderPriorities(SecurityLens())
	wantSec := "policy=1.00, runtime=0.80, graph=0.60, git=0.50, test=0.40, build=0.30, memory=0.20"
	if sec != wantSec {
		t.Errorf("RenderPriorities(security) = %q, want %q", sec, wantSec)
	}
	bal := RenderPriorities(BalancedLens())
	wantBal := "build=1.00, git=1.00, graph=1.00, memory=1.00, policy=1.00, runtime=1.00, test=1.00"
	if bal != wantBal {
		t.Errorf("RenderPriorities(balanced) = %q, want %q (weight ties sorted by type)", bal, wantBal)
	}
}

func TestApplyLensEmpty(t *testing.T) {
	var nilClaims []domain.Claim
	if got := ApplyLens(SecurityLens(), nilClaims); got != nil {
		t.Errorf("nil claims should stay nil, got %v", got)
	}
	if got := ApplyLens(SecurityLens(), []domain.Claim{}); len(got) != 0 {
		t.Errorf("empty claims should stay empty, got %v", got)
	}
}

func TestResolve(t *testing.T) {
	// Single name selects the built-in lens.
	l, err := Resolve("security")
	if err != nil {
		t.Fatalf("Resolve(security): %v", err)
	}
	if l.Name != "security" || len(l.Priorities) == 0 {
		t.Errorf("Resolve(security) = %+v, want builtin security lens", l)
	}
	// Combined names merge via CombinedLens (2 and 3 parts).
	two, err := Resolve("security+maintainability")
	if err != nil {
		t.Fatalf("Resolve(security+maintainability): %v", err)
	}
	if two.Name != "security+maintainability" {
		t.Errorf("combined Name = %q, want security+maintainability", two.Name)
	}
	if len(two.Priorities) == 0 {
		t.Error("combined lens should merge priorities")
	}
	three, err := Resolve("performance+security+architecture")
	if err != nil {
		t.Fatalf("Resolve(performance+security+architecture): %v", err)
	}
	if three.Name != "performance+security+architecture" {
		t.Errorf("3-part combined Name = %q", three.Name)
	}
	// "," is an equivalent separator, with spaces trimmed.
	comma, err := Resolve("security, maintainability")
	if err != nil {
		t.Fatalf("Resolve(security, maintainability): %v", err)
	}
	if comma.Name != "security+maintainability" {
		t.Errorf("comma-separated Name = %q, want security+maintainability", comma.Name)
	}
	if got, want := comma.Name, two.Name; got != want {
		t.Errorf("separator normalization: got %q, want %q", got, want)
	}
}

func TestResolveErrors(t *testing.T) {
	if _, err := Resolve("bogus"); err == nil || !strings.Contains(err.Error(), "unknown lens") {
		t.Errorf("Resolve(bogus) err = %v, want unknown-lens error", err)
	}
	if _, err := Resolve("bogus"); err == nil || !strings.Contains(err.Error(), "security") {
		t.Errorf("Resolve(bogus) err = %v, want available-lens list", err)
	}
	if _, err := Resolve("security+bogus"); err == nil || !strings.Contains(err.Error(), `unknown lens "bogus"`) {
		t.Errorf("Resolve(security+bogus) err = %v, want unknown part", err)
	}
	if _, err := Resolve(""); err == nil || !strings.Contains(err.Error(), "empty lens name") {
		t.Errorf("Resolve(empty) err = %v, want empty-name error", err)
	}
	if _, err := Resolve("+"); err == nil || !strings.Contains(err.Error(), "empty lens name") {
		t.Errorf("Resolve(+) err = %v, want empty-name error", err)
	}
	if _, err := Resolve("security++maintainability, ,"); err != nil {
		t.Errorf("Resolve with stray separators: %v", err)
	}
}
