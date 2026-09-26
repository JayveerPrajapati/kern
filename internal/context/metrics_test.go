package context

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

func TestMeasureBasics(t *testing.T) {
	pkt := domain.ContextPacket{TokenCount: 100}
	m := Measure(pkt, 3*time.Millisecond)

	if m.TokenReduction <= 0 {
		t.Errorf("TokenReduction = %v, want > 0", m.TokenReduction)
	}
	// With raw = 4x packet: reduction = (400-100)/400 = 75%.
	if m.TokenReduction < 74 || m.TokenReduction > 76 {
		t.Errorf("TokenReduction = %v, want ~75", m.TokenReduction)
	}
	if m.Latency != 3*time.Millisecond {
		t.Errorf("Latency = %v, want 3ms", m.Latency)
	}
	if m.Cost <= 0 {
		t.Errorf("Cost = %v, want > 0", m.Cost)
	}
	if m.RetrievalRelevance != 100 {
		t.Errorf("RetrievalRelevance = %v, want 100 when no facts", m.RetrievalRelevance)
	}
}

func TestMeasureZeroTokens(t *testing.T) {
	m := Measure(domain.ContextPacket{TokenCount: 0}, 0)
	if m.TokenReduction != 0 {
		t.Errorf("TokenReduction = %v, want 0 for empty packet", m.TokenReduction)
	}
}

func TestMeasureRetrievalRelevance(t *testing.T) {
	pkt := domain.ContextPacket{
		TokenCount: 100,
		Facts: []domain.Claim{
			{Statement: "a", Evidence: []domain.Evidence{{Type: domain.EvidenceGraph}}},
			{Statement: "b"}, // no evidence
		},
	}
	m := Measure(pkt, time.Nanosecond)
	// 1 of 2 facts has evidence -> 50%.
	if m.RetrievalRelevance != 50 {
		t.Errorf("RetrievalRelevance = %v, want 50", m.RetrievalRelevance)
	}
}

func TestMeasureCostScalesWithTokens(t *testing.T) {
	a := Measure(domain.ContextPacket{TokenCount: 10}, 0)
	b := Measure(domain.ContextPacket{TokenCount: 20}, 0)
	if !(b.Cost > a.Cost) {
		t.Errorf("Cost should scale with tokens: %v -> %v", a.Cost, b.Cost)
	}
}

func TestDefaultCostPerToken(t *testing.T) {
	if CostPerToken() <= 0 {
		t.Errorf("default costPerToken = %v, want > 0", CostPerToken())
	}
}

func TestCostPerTokenFromEnv(t *testing.T) {
	t.Setenv("KERN_COST_PER_TOKEN", "0.001")
	tokens := 100
	m := Measure(domain.ContextPacket{TokenCount: tokens}, 0)
	want := float64(tokens) * 0.001
	if m.Cost != want {
		t.Errorf("Cost = %v, want %v (tokens * env rate)", m.Cost, want)
	}
}

func TestCostPerTokenZeroDisablesCost(t *testing.T) {
	t.Setenv("KERN_COST_PER_TOKEN", "0")
	m := Measure(domain.ContextPacket{TokenCount: 100}, 0)
	if m.Cost != 0 {
		t.Errorf("Cost = %v, want 0 when rate is 0", m.Cost)
	}
}

func TestCostPerTokenForModelTable(t *testing.T) {
	cases := []struct {
		model string
		want  float64
	}{
		{"gpt-4o", 2.50 / 1e6},
		{"gpt-4o-2024-08-06", 2.50 / 1e6}, // suffix after prefix still matches
		{"gpt-4o-mini", 0.15 / 1e6},       // longest prefix wins
		{"claude-sonnet-4-5", 3.00 / 1e6}, // family prefix
		{"Claude-Sonnet-4-5", 3.00 / 1e6}, // case-insensitive
		{"llama3.1:8b", 0},                // local-first family: no spend
		{"totally-unknown-model", defaultCostPerToken},
		{"", defaultCostPerToken}, // no model → model-agnostic chain
	}
	for _, c := range cases {
		if got := CostPerTokenFor(c.model); got != c.want {
			t.Errorf("CostPerTokenFor(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

func TestModelCostsEnvOverride(t *testing.T) {
	t.Setenv("KERN_MODEL_COSTS", "gpt-4o=5, my-private-model=1.25, garbage-entry, bad=not-a-number")
	if got := CostPerTokenFor("gpt-4o"); got != 5.0/1e6 {
		t.Errorf("custom gpt-4o rate = %v, want $5/1M", got)
	}
	if got := CostPerTokenFor("my-private-model-v2"); got != 1.25/1e6 {
		t.Errorf("custom model rate = %v, want $1.25/1M", got)
	}
	// Garbage entries must not break the built-in table.
	if got := CostPerTokenFor("claude-sonnet-4"); got != 3.00/1e6 {
		t.Errorf("built-in claude-sonnet rate = %v, want $3/1M", got)
	}
}

func TestCostPerTokenPrecedence(t *testing.T) {
	// An explicit operator rate beats the per-model table.
	t.Setenv("KERN_COST_PER_TOKEN", "0.002")
	if got := CostPerTokenFor("gpt-4o"); got != 0.002 {
		t.Errorf("operator rate = %v, want 0.002 (env beats table)", got)
	}
}

func TestMeasureForModelCost(t *testing.T) {
	tokens := 1000
	m := MeasureFor(domain.ContextPacket{TokenCount: tokens}, 0, "gpt-4o")
	want := float64(tokens) * 2.50 / 1e6
	if m.Cost != want {
		t.Errorf("MeasureFor cost = %v, want %v", m.Cost, want)
	}
	// The model-aware estimate must differ from the model-agnostic one.
	plain := Measure(domain.ContextPacket{TokenCount: tokens}, 0)
	if plain.Cost == m.Cost {
		t.Errorf("model-aware cost %v should differ from default %v", m.Cost, plain.Cost)
	}
}

// TestEffectiveCostPerTokenUsesOperatorModel pins FIX 3: the aggregate cost
// chain must engage the per-model table for the operator's configured model
// (KERN_LLM_MODEL, the accepted alias for the LLM factory's KERN_MODEL)
// instead of always falling to the flat 1e-05 default.
func TestEffectiveCostPerTokenUsesOperatorModel(t *testing.T) {
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "gpt-4o")
	if got := CostPerToken(); got != 2.50/1e6 {
		t.Fatalf("CostPerToken with KERN_LLM_MODEL=gpt-4o = %v, want 2.50/1M", got)
	}
	perMillion, model := CostRate()
	if perMillion != 2.50 || model != "gpt-4o" {
		t.Fatalf("CostRate = (%.4g, %q), want (2.5, gpt-4o)", perMillion, model)
	}
}

// TestEffectiveCostPerTokenFlatDefaultWhenUnset pins the unchanged fallback:
// with no model configured the flat 1e-05 default applies.
func TestEffectiveCostPerTokenFlatDefaultWhenUnset(t *testing.T) {
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "")
	if got := CostPerToken(); got != defaultCostPerToken {
		t.Fatalf("CostPerToken with no model = %v, want default %v", got, defaultCostPerToken)
	}
}

// TestEffectiveCostPerTokenOverrideWins pins the precedence: an explicit
// operator rate (KERN_COST_PER_TOKEN) beats the per-model table even when a
// model is configured.
func TestEffectiveCostPerTokenOverrideWins(t *testing.T) {
	t.Setenv("KERN_COST_PER_TOKEN", "0.001")
	t.Setenv("KERN_LLM_MODEL", "gpt-4o")
	t.Setenv("KERN_MODEL", "")
	if got := CostPerToken(); got != 0.001 {
		t.Fatalf("operator override = %v, want 0.001 (beats the model table)", got)
	}
}

// TestCostRateInfoKinds pins the surfacing derivation behind the "cost
// model" line: the per-model table when a model is configured, the operator
// override when one is set, and the labeled flat assumption when nothing is
// known — so a fresh install's 1e-05 is never presented as a confident
// figure.
func TestCostRateInfoKinds(t *testing.T) {
	// Table: a configured model engages the per-model table.
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "gpt-4o")
	perMillion, model, kind := CostRateInfo()
	if perMillion != 2.50 || model != "gpt-4o" || kind != "table" {
		t.Fatalf("CostRateInfo = (%.4g, %q, %q), want (2.5, gpt-4o, table)", perMillion, model, kind)
	}

	// Override: an explicit operator rate wins and is labeled as such.
	t.Setenv("KERN_COST_PER_TOKEN", "0.001")
	perMillion, _, kind = CostRateInfo()
	if perMillion != 1000 || kind != "override" {
		t.Fatalf("CostRateInfo override = (%.4g, %q), want (1000, override)", perMillion, kind)
	}

	// Flat assumption: no model, no override — the 1e-05 default with the
	// flat kind and no model tier.
	t.Setenv("KERN_COST_PER_TOKEN", "")
	t.Setenv("KERN_MODEL", "")
	t.Setenv("KERN_LLM_MODEL", "")
	perMillion, model, kind = CostRateInfo()
	if perMillion != defaultCostPerToken*1e6 || model != "" || kind != "flat" {
		t.Fatalf("CostRateInfo flat = (%.4g, %q, %q), want (10, \"\", flat)", perMillion, model, kind)
	}

	// A configured model with no table entry is also a labeled flat
	// assumption (the model is named so the label can point at it).
	t.Setenv("KERN_MODEL", "my-private-unknown-model")
	perMillion, model, kind = CostRateInfo()
	if perMillion != defaultCostPerToken*1e6 || model != "my-private-unknown-model" || kind != "flat" {
		t.Fatalf("CostRateInfo unknown-model = (%.4g, %q, %q), want (10, model, flat)", perMillion, model, kind)
	}
}
