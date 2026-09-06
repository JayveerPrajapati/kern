package memory

import (
	"math"
	"time"
)

// RankedEntry pairs a lesson Entry with score components for explainability.
type RankedEntry struct {
	Entry     Entry   `json:"entry"`
	Score     float64 `json:"score"`
	Relevance float64 `json:"relevance"`
	Recency   float64 `json:"recency"`
	AgeHours  float64 `json:"age_hours"`
}

// RecallRanked returns the top-k lessons for prompt, weighted by semantic token
// relevance and exponential time decay.
//
// halfLifeDays controls the decay rate. If <= 0, a default of 7.0 days is used.
// A lesson aged exactly halfLifeDays receives a recency multiplier of 0.5.
func RecallRanked(root, prompt string, k int, halfLifeDays float64) []RankedEntry {
	if k <= 0 {
		k = 5
	}
	if halfLifeDays <= 0 {
		halfLifeDays = 7.0
	}

	ptoks := tokens(prompt)
	if len(ptoks) == 0 {
		return nil
	}

	entries := List(root)
	if len(entries) == 0 {
		return nil
	}

	now := time.Now().UTC()
	// lambda = ln(2) / halfLifeHours
	halfLifeHours := halfLifeDays * 24.0
	decayLambda := math.Ln2 / halfLifeHours

	var pool []RankedEntry
	for _, e := range entries {
		if e.Source == "auto" {
			continue
		}
		etoks := tokens(e.Text)
		if len(etoks) == 0 {
			continue
		}

		set := map[string]bool{}
		for _, t := range etoks {
			set[t] = true
		}

		overlap := 0
		for _, t := range ptoks {
			if set[t] {
				overlap++
			}
		}

		if overlap == 0 {
			continue
		}

		// Relevance score based on token coverage
		rel := float64(overlap)/float64(len(ptoks)) + float64(overlap)/float64(len(etoks))*0.5

		// Recency factor with exponential decay
		age := now.Sub(e.Time)
		if age < 0 {
			age = 0
		}
		ageHours := age.Hours()
		recency := math.Exp(-decayLambda * ageHours)

		// Combined score: base relevance scaled by recency (with a 0.3 floor so old foundational lessons still surface)
		finalScore := rel * (0.3 + 0.7*recency)

		pool = append(pool, RankedEntry{
			Entry:     e,
			Score:     math.Round(finalScore*1000) / 1000,
			Relevance: math.Round(rel*1000) / 1000,
			Recency:   math.Round(recency*1000) / 1000,
			AgeHours:  math.Round(ageHours*10) / 10,
		})
	}

	// Sort descending by score
	for i := 0; i < len(pool); i++ {
		for j := i + 1; j < len(pool); j++ {
			if pool[j].Score > pool[i].Score {
				pool[i], pool[j] = pool[j], pool[i]
			}
		}
	}

	if len(pool) > k {
		pool = pool[:k]
	}
	return pool
}
