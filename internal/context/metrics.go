package context

import (
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// costPerToken is a placeholder unit cost used to estimate spend.
const costPerToken = 0.00001 // $ per token (approximate, placeholder)

// Metrics tracks context engine performance.
type Metrics struct {
	TokenReduction     float64       // % reduction vs raw context
	RetrievalRelevance float64       // % of retrieved items relevant (placeholder heuristic)
	Latency            time.Duration // time to assemble the packet
	Cost               float64       // estimated cost (placeholder)
}

// Measure analyzes a ContextPacket and returns deterministic placeholder
// heuristics: TokenReduction against a ~4x raw-context baseline, a relevance
// proxy (share of facts carrying evidence), and Cost scaled by costPerToken.
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
		Cost:               float64(pkt.TokenCount) * costPerToken,
	}
}
