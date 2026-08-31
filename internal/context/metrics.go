package context

import (
	"os"
	"strconv"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// defaultCostPerToken is the $/token used to estimate spend when
// KERN_COST_PER_TOKEN is unset or invalid. It is the default LLM cost rate
// operators can override with their provider's real rate.
const defaultCostPerToken = 0.00001

// costPerToken holds an explicit runtime override (see SetCostPerToken).
// When unset, the effective rate is re-derived from the KERN_COST_PER_TOKEN
// environment variable each time it is needed, so operators (or tests using
// t.Setenv) can change it without a code change.
var (
	costPerToken    float64
	costPerTokenSet bool
)

// effectiveCostPerToken returns the $/token rate used to estimate spend: an
// explicit override if one was set, else the KERN_COST_PER_TOKEN env var
// (parsed as a float64 >= 0), else defaultCostPerToken.
func effectiveCostPerToken() float64 {
	if costPerTokenSet {
		return costPerToken
	}
	if v := os.Getenv("KERN_COST_PER_TOKEN"); v != "" {
		if rate, err := strconv.ParseFloat(v, 64); err == nil && rate >= 0 {
			return rate
		}
	}
	return defaultCostPerToken
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
// costPerToken rate (see CostPerToken / KERN_COST_PER_TOKEN).
func Measure(pkt domain.ContextPacket, assembleDuration time.Duration) Metrics {
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
		Cost:               float64(pkt.TokenCount) * effectiveCostPerToken(),
	}
}
