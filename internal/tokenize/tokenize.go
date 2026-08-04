// Package tokenize provides local, offline token counting for prompts.
//
// It uses a character/word based estimator that is consistent between the
// "before" and "after" versions of a prompt, so token savings percentages are
// accurate even though absolute counts are approximate. A pluggable interface
// allows swapping in an exact BPE tokenizer later without changing callers.
package tokenize

import "strings"

// Kind describes the rough content type, which affects density assumptions.
type Kind int

const (
	// KindGeneric is mixed prose (default).
	KindGeneric Kind = iota
	// KindCode is source code (more tokens per character).
	KindCode
	// KindLog is log output (very noisy, lots of tokens per line).
	KindLog
)

// Counter counts tokens for a piece of text.
type Counter interface {
	Count(s string) int
}

// Estimator is the default deterministic counter.
type Estimator struct {
	Kind Kind
}

var defaultTokensPerChar = map[Kind]float64{
	KindGeneric: 1.0 / 4.0,
	KindCode:    1.0 / 3.5,
	KindLog:     1.0 / 3.2,
}

// Count returns the estimated number of tokens in s.
func (e Estimator) Count(s string) int {
	if s == "" {
		return 0
	}
	// Split on whitespace to weight identifiers/words more heavily than
	// whitespace runs. This cheaply captures the "code is denser" effect.
	fields := strings.Fields(s)
	n := 0
	for _, f := range fields {
		n += len(f)
	}
	// Add a per-field overhead (each token boundary costs roughly one token).
	n += len(fields)

	factor, ok := defaultTokensPerChar[e.Kind]
	if !ok {
		factor = defaultTokensPerChar[KindGeneric]
	}
	est := float64(n) * factor
	if est < 1 {
		est = 1
	}
	return int(est)
}

// Count is a convenience wrapper using the generic estimator.
func Count(s string) int {
	return (Estimator{Kind: KindGeneric}).Count(s)
}

// CountKind is a convenience wrapper with an explicit content kind.
func CountKind(s string, k Kind) int {
	return (Estimator{Kind: k}).Count(s)
}
