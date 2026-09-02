// Package tokenize provides local, offline token counting for prompts.
// It uses a character/word based estimator that is consistent between the
// "before" and "after" versions of a prompt, so token savings percentages are
// accurate even though absolute counts are approximate. A pluggable interface
// allows swapping in an exact BPE tokenizer later without changing callers.
//
// The package-level Count/CountKind functions delegate to a configurable
// default counter (see SetDefault, InitFromEnv). By default — and for every
// release before this mechanism existed — that is the Estimator, so all
// reported numbers stay stable unless a caller opts into an exact tokenizer
// (NewCl100kCounter/NewO200kCounter, or KERN_TOKENIZER=cl100k|o200k|bpe).
package tokenize

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

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

// Count is a convenience wrapper using the active default counter.
func Count(s string) int {
	return Default().Count(s)
}

// CountKind is a convenience wrapper with an explicit content kind. The
// kind only affects heuristic counters (Estimator); exact tokenizers
// ignore it.
func CountKind(s string, k Kind) int {
	c := Default()
	if kc, ok := c.(kindCounter); ok {
		return kc.countKind(s, k)
	}
	return c.Count(s)
}

// kindCounter is implemented by counters whose counting exploits the
// content Kind.
type kindCounter interface {
	countKind(s string, k Kind) int
}

func (e Estimator) countKind(s string, k Kind) int {
	return Estimator{Kind: k}.Count(s)
}

// ---------------------------------------------------------------------------
// Default-counter selection.
//
// The default is the Estimator. Callers can swap in an exact tokenizer
// with SetDefault, or let the environment decide (InitFromEnv, or the
// lazy resolution performed by the first Count call):
//
//	KERN_TOKENIZER = estimator | bpe | cl100k | o200k
//	    (aliases: cl100k_base, o200k_base)
//	KERN_MODEL     = model name; gpt-4o*/o1*/o3* select o200k,
//	                 gpt-4*/gpt-3.5* select cl100k, anything else keeps
//	                 the estimator
//
// KERN_TOKENIZER wins over KERN_MODEL. An unknown value or a failed
// table load falls back to the Estimator with a one-line warning.

var (
	defaultMu       sync.RWMutex
	defaultCounter  Counter = Estimator{Kind: KindGeneric}
	defaultResolved bool
)

// Default returns the active default counter, resolving it from the
// environment on first use.
func Default() Counter {
	defaultMu.RLock()
	if defaultResolved {
		c := defaultCounter
		defaultMu.RUnlock()
		return c
	}
	defaultMu.RUnlock()

	defaultMu.Lock()
	defer defaultMu.Unlock()
	if !defaultResolved {
		defaultCounter = resolveFromEnv()
		defaultResolved = true
	}
	return defaultCounter
}

// SetDefault replaces the active default counter. A nil counter resets
// to the Estimator. Explicitly set counters suppress environment
// resolution.
func SetDefault(c Counter) {
	if c == nil {
		c = Estimator{Kind: KindGeneric}
	}
	defaultMu.Lock()
	defaultCounter, defaultResolved = c, true
	defaultMu.Unlock()
}

// InitFromEnv re-resolves the default counter from KERN_TOKENIZER and
// KERN_MODEL (useful after env changes in tests or long-lived servers).
func InitFromEnv() {
	defaultMu.Lock()
	defer defaultMu.Unlock()
	defaultCounter = resolveFromEnv()
	defaultResolved = true
}

// ResetDefault restores the pre-configuration behavior (Estimator) and
// re-arms environment resolution. Primarily for tests.
func ResetDefault() {
	defaultMu.Lock()
	defaultCounter = Estimator{Kind: KindGeneric}
	defaultResolved = false
	defaultMu.Unlock()
}

func resolveFromEnv() Counter {
	est := Estimator{Kind: KindGeneric}
	if v := strings.TrimSpace(os.Getenv("KERN_TOKENIZER")); v != "" {
		switch strings.ToLower(v) {
		case "estimator":
			return est
		case "bpe":
			return NewBPECounter()
		case "cl100k", "cl100k_base", "tiktoken-cl100k":
			if c, err := NewCl100kCounter(); err == nil {
				return c
			} else {
				fmt.Fprintf(os.Stderr, "kern/tokenize: KERN_TOKENIZER=%q unavailable (%v); using estimator\n", v, err)
			}
		case "o200k", "o200k_base", "tiktoken-o200k":
			if c, err := NewO200kCounter(); err == nil {
				return c
			} else {
				fmt.Fprintf(os.Stderr, "kern/tokenize: KERN_TOKENIZER=%q unavailable (%v); using estimator\n", v, err)
			}
		default:
			fmt.Fprintf(os.Stderr, "kern/tokenize: unknown KERN_TOKENIZER=%q (want estimator|bpe|cl100k|o200k); using estimator\n", v)
		}
		return est
	}
	model := strings.ToLower(os.Getenv("KERN_MODEL"))
	if model == "" {
		return est
	}
	switch {
	case strings.Contains(model, "gpt-4o"),
		strings.Contains(model, "chatgpt-4o"),
		strings.HasPrefix(model, "o1"),
		strings.HasPrefix(model, "o3"),
		strings.HasPrefix(model, "o4"):
		if c, err := NewO200kCounter(); err == nil {
			return c
		}
	case strings.Contains(model, "gpt-4"),
		strings.Contains(model, "gpt-3.5"),
		strings.Contains(model, "gpt-35"),
		strings.Contains(model, "gpt3.5"):
		if c, err := NewCl100kCounter(); err == nil {
			return c
		}
	}
	return est
}
