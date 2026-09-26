package context

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/config"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// defaultCostPerToken is the $/token used to estimate spend when
// KERN_COST_PER_TOKEN is unset or invalid. It is the default LLM cost rate
// operators can override with their provider's real rate.
const defaultCostPerToken = 0.00001

// modelCostsPerMillion maps model-family prefixes to an approximate blended
// $/1M-token list rate (input-side, vendor-published prices rounded; these
// are ESTIMATES for spend telemetry, never billing). Lookup is
// longest-prefix on the lowercased model name, so "gpt-4o-2024-08-06"
// matches "gpt-4o" and "gpt-4o-mini" wins over "gpt-4o". The local-first
// families (llama, gemma, phi, codellama) default to 0: models commonly run
// on a local ollama carry no per-token spend. Operators override or extend
// the table with KERN_MODEL_COSTS ("name=price,name=price", price in $/1M
// tokens), which beats the built-in table per prefix.
var modelCostsPerMillion = map[string]float64{
	// OpenAI
	"gpt-5":        1.25,
	"gpt-4.1":      2.00,
	"gpt-4.1-mini": 0.40, // longer prefix beats "gpt-4.1"
	"gpt-4.1-nano": 0.10, // longer prefix beats "gpt-4.1"
	"gpt-4o-mini":  0.15,
	"gpt-4o":       2.50,
	"o3-mini":      1.10, // longer prefix beats "o3" (10.00)
	"o3":           10.00,
	"o1":           15.00,
	// Anthropic
	"claude-opus-4":     15.00,
	"claude-sonnet":     3.00,
	"claude-3-5-sonnet": 3.00, // no shorter prefix covers the 3.5 line
	"claude-haiku":      0.80,
	"claude-3-5-haiku":  0.80, // no shorter prefix covers the 3.5 line
	"claude-haiku-4-5":  1.00, // longer prefix beats "claude-haiku"
	// Google
	"gemini-2.5-pro":   1.25,
	"gemini-2.5-flash": 0.30,
	"gemini-1.5-flash": 0.075,
	"gemini-flash":     0.30,
	// Others (hosted)
	"deepseek":      0.27,
	"deepseek-r1":   0.55, // more specific than the blended "deepseek"
	"deepseek-v3":   0.14, // more specific than the blended "deepseek"
	"mistral-large": 2.00,
	"mistral":       0.50,
	"qwen":          0.40,
	// Local-first families (commonly local via ollama): no per-token spend.
	"llama":     0,
	"gemma":     0,
	"phi":       0,
	"codellama": 0,
	"local":     0, // explicit local sentinel
}

// costPerToken holds an explicit runtime override (see SetCostPerToken).
// When unset, the effective rate is re-derived from KERN_COST_PER_TOKEN (or
// cost_per_token in .kern/config.json) each time it is needed, so operators
// (or tests using t.Setenv) can change it without a code change.
var (
	costPerToken    float64
	costPerTokenSet bool
)

// operatorModel resolves the operator's LLM model name the same way the LLM
// factory does (internal/llm: KERN_MODEL env, else llm.model in
// .kern/config.json), with KERN_LLM_MODEL accepted as an alias so the
// model-name convention used across kern surfaces works here too. Empty when
// nothing is configured — the model-agnostic flat rate then applies.
func operatorModel() string {
	if m := config.String("", "KERN_MODEL", "llm.model", ""); m != "" {
		return m
	}
	return strings.TrimSpace(os.Getenv("KERN_LLM_MODEL"))
}

// effectiveCostPerToken returns the $/token rate used to estimate spend: an
// explicit override if one was set, else KERN_COST_PER_TOKEN / cost_per_token
// (env > file; parsed as a float64 >= 0), else the per-model table entry for
// the operator's configured model, else defaultCostPerToken. The operator's
// model is resolved the same way the LLM factory does, so the per-model table
// engages for a configured model instead of always falling to the flat
// 1e-05 default.
func effectiveCostPerToken() float64 {
	rate, _, _ := resolveCost()
	return rate
}

// resolveCost returns the effective $/token spend rate with its derivation
// for surfacing: the rate, the model tier it was derived from ("" when none),
// and a kind label describing where the rate came from. It mirrors
// costPerTokenFor's precedence chain exactly (runtime override > explicit
// operator rate > per-model table > default) but also reports WHY, so a
// fresh install's flat 1e-05 can be labeled as an assumption instead of
// presenting a bare number as a confident figure:
//
//	"override" — an explicit rate was set (SetCostPerToken, KERN_COST_PER_TOKEN
//	            or cost_per_token in .kern/config.json)
//	"table"    — the per-model table entry for the operator's configured model
//	"flat"     — the 1e-05 default, an assumption: either no model is
//	            configured (model == "") or the configured model has no table
//	            entry
func resolveCost() (rate float64, model, kind string) {
	if costPerTokenSet {
		return costPerToken, "", "override"
	}
	if r := config.Float64("", "KERN_COST_PER_TOKEN", "cost_per_token", -1); r >= 0 {
		return r, "", "override"
	}
	model = operatorModel()
	if model != "" {
		if r, ok := lookupModelRate(strings.ToLower(model)); ok {
			return r, model, "table"
		}
		return defaultCostPerToken, model, "flat"
	}
	return defaultCostPerToken, "", "flat"
}

// costPerTokenFor returns the $/token rate for a specific model: precedence
// is runtime override (SetCostPerToken) > explicit operator rate
// (KERN_COST_PER_TOKEN / cost_per_token) > the per-model table (built-in,
// overridden per-prefix by KERN_MODEL_COSTS) > defaultCostPerToken. An empty
// model skips the table lookup.
func costPerTokenFor(model string) float64 {
	if costPerTokenSet {
		return costPerToken
	}
	if rate := config.Float64("", "KERN_COST_PER_TOKEN", "cost_per_token", -1); rate >= 0 {
		return rate
	}
	if model != "" {
		if rate, ok := lookupModelRate(strings.ToLower(model)); ok {
			return rate
		}
	}
	return defaultCostPerToken
}

// mergedModelRates returns the effective per-model rate table in $/1M
// tokens: the built-in modelCostsPerMillion overlaid by KERN_MODEL_COSTS
// entries (custom winning per-prefix). Shared by lookupModelRate and
// ModelRates so both surfaces always see the identical table.
func mergedModelRates() map[string]float64 {
	merged := make(map[string]float64, len(modelCostsPerMillion)+4)
	for prefix, rate := range modelCostsPerMillion {
		merged[prefix] = rate
	}
	for prefix, rate := range customModelCosts() {
		merged[prefix] = rate // custom entries override the built-in table
	}
	return merged
}

// lookupModelRate finds the longest-prefix match for model in the cost
// table (built-in modelCostsPerMillion overlaid by KERN_MODEL_COSTS
// entries, custom winning per-prefix), returning the $/token rate.
func lookupModelRate(model string) (float64, bool) {
	merged := mergedModelRates()
	best, bestLen := 0.0, -1
	for prefix, perMillion := range merged {
		if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
			best, bestLen = perMillion, len(prefix)
		}
	}
	if bestLen < 0 {
		return 0, false
	}
	return best / 1e6, true
}

// customModelCosts parses KERN_MODEL_COSTS ("name=price,name=price", price
// in $/1M tokens) into a prefix→rate map. Garbage entries are skipped.
func customModelCosts() map[string]float64 {
	raw := strings.TrimSpace(os.Getenv("KERN_MODEL_COSTS"))
	if raw == "" {
		return nil
	}
	out := map[string]float64{}
	for _, entry := range strings.Split(raw, ",") {
		parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
		if len(parts) != 2 {
			continue
		}
		rate, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil || rate < 0 {
			continue
		}
		out[strings.ToLower(strings.TrimSpace(parts[0]))] = rate
	}
	return out
}

// SetCostPerToken overrides the $ per token rate used to estimate spend.
// A rate of 0 disables cost estimation (Measure will report Cost == 0).
func SetCostPerToken(rate float64) {
	costPerToken = rate
	costPerTokenSet = true
}

// CostPerToken returns the current $ per token rate used to estimate spend.
func CostPerToken() float64 {
	return effectiveCostPerToken()
}

// Metrics tracks context engine performance.
type Metrics struct {
	TokenReduction     float64       // % reduction vs raw context
	RetrievalRelevance float64       // % of retrieved items relevant (placeholder heuristic)
	Latency            time.Duration // time to assemble the packet
	Cost               float64       // estimated cost = TokenCount * costPerToken
}

// Measure analyzes a ContextPacket and returns deterministic heuristics:
// TokenReduction against a ~4x raw-context baseline, a relevance proxy (share
// of facts carrying evidence), and Cost scaled by the configurable
// costPerToken rate (see CostPerToken / KERN_COST_PER_TOKEN). No model is
// known, so the model-agnostic rate chain applies.
func Measure(pkt domain.ContextPacket, assembleDuration time.Duration) Metrics {
	return MeasureFor(pkt, assembleDuration, "")
}

// MeasureFor is Measure with the LLM model named: Cost is estimated with the
// per-model rate table (see modelCostsPerMillion / KERN_MODEL_COSTS) instead
// of the single model-agnostic default — Persona 8's $0.23-actual vs
// ~$0.06-estimated gap was exactly this conflation.
func MeasureFor(pkt domain.ContextPacket, assembleDuration time.Duration, model string) Metrics {
	// TokenReduction: assume the raw context would be ~4x the optimized packet.
	reduction := 0.0
	if pkt.TokenCount > 0 {
		raw := pkt.TokenCount * 4
		reduction = (1 - float64(pkt.TokenCount)/float64(raw)) * 100
	}

	// RetrievalRelevance: fraction of facts carrying supporting evidence.
	relevance := 100.0
	if total := len(pkt.Facts); total > 0 {
		backed := 0
		for _, c := range pkt.Facts {
			if c.HasEvidence() {
				backed++
			}
		}
		relevance = float64(backed) / float64(total) * 100
	}

	return Metrics{
		TokenReduction:     reduction,
		RetrievalRelevance: relevance,
		Latency:            assembleDuration,
		Cost:               float64(pkt.TokenCount) * costPerTokenFor(model),
	}
}

// CostPerTokenFor returns the $/token rate used to estimate spend for the
// given model (empty = the model-agnostic chain). Precedence: runtime
// override > KERN_COST_PER_TOKEN / cost_per_token > per-model table
// (built-in, extended per-prefix by KERN_MODEL_COSTS) > default.
func CostPerTokenFor(model string) float64 {
	return costPerTokenFor(model)
}

// CostPerMillionFor returns the $/1M-token rate for a specific model via
// the same chain as CostPerTokenFor (runtime override > KERN_COST_PER_TOKEN
// / cost_per_token > per-model table > flat default). Unknown and empty
// models fall to the flat assumed default ($1e-05/token = $10/1M), so
// per-entry savings stay coherent with the aggregate "cost model" label.
// It is the canonical resolver for the stats ledger, which previously kept
// a divergent exact-match table of its own.
func CostPerMillionFor(model string) float64 {
	return costPerTokenFor(model) * 1e6
}

// ModelRates returns the merged per-model rate table in $/1M tokens
// (built-in prefixes overlaid by KERN_MODEL_COSTS entries, custom winning
// per-prefix), for surfaces that enumerate rates rather than resolve one
// (e.g. stats.ModelSavings). The map is a fresh copy on every call.
func ModelRates() map[string]float64 {
	return mergedModelRates()
}

// CostRate returns the effective spend rate as $/1M tokens and the model
// tier it was derived from ("" when no model is configured and the flat
// default applies). It powers the self-explaining cost line surfaces
// ("cost_rate: 2.50/1M (gpt-4o)") so an estimated spend is never a bare
// number.
func CostRate() (perMillion float64, model string) {
	perMillion, model, _ = CostRateInfo()
	return perMillion, model
}

// CostRateInfo returns the effective spend rate and its derivation for
// surfacing: perMillion ($/1M tokens), model (the tier it was derived from,
// "" when none), and kind ("override" | "table" | "flat" — see resolveCost).
// Surfaces use kind to label a flat 1e-05 as an assumption ("set llm.model
// or cost_per_token") instead of presenting it as a confident figure.
func CostRateInfo() (perMillion float64, model, kind string) {
	rate, model, kind := resolveCost()
	return rate * 1e6, model, kind
}
