// Package evidence wraps deterministic v1 outputs (intel, sec, verify) as
// typed domain.Claim objects carrying evidence, provenance and confidence,
// built via a fluent Builder and per-output factory functions.
package evidence

// Confidence constants: deterministic observations are facts and get
// ConfidenceCertain; derived conclusions carry a lower score based on how
// directly they follow from the underlying facts.
const (
	// ConfidenceCertain is used for deterministic observations (1.0).
	ConfidenceCertain = 1.0
	// ConfidenceHigh is used for inferences directly derived from facts (0.9).
	ConfidenceHigh = 0.9
	// ConfidenceModerate is used for inferences over historical or partial
	// data, such as recalled memory (0.8).
	ConfidenceModerate = 0.8
)
