// LoopDetector (B4, anti-stall campaign Phase 1): advisory detection of
// degenerate read loops — a read-tool query that RESEMBLES an earlier one in
// the same session (near-duplicates that bypass the exact-match served
// ledger: "read → still unsure → re-read with slightly different args").
// Purely advisory: it never denies or alters a call; the MCP layer appends a
// one-line marker when a near-duplicate is flagged. Deterministic,
// stdlib-only, no LLM in the loop.
//
// State is kept per session key: a FIFO ring of the last LoopRingSize
// normalized queries plus the set of earlier queries already flagged, so
// each (session key, matched earlier query) pair is flagged at most once —
// the marker never nags on every subsequent near-duplicate.
package semcache

import (
	"os"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// LoopRingSize bounds one session's query ring: 32 recent queries covers a
// working session's read history; the oldest are evicted first.
const LoopRingSize = 32

// LoopSimThreshold is the jaccard similarity at/above which a query is a
// loop suspect (0.80 — strong token overlap, well above paraphrase noise).
const LoopSimThreshold = 0.80

// loopMaxTokens bounds one query's token-set signature (deterministic cap;
// real read queries are far smaller).
const loopMaxTokens = 128

// LoopSuspect is one flagged near-duplicate: the earlier call's sequence
// number, the similarity, and the earlier normalized query text.
type LoopSuspect struct {
	CallSeq int
	Sim     float64
	Query   string
}

// Normalize folds a read-tool call into its comparable query text: tool + " "
// + the primary subject argument, tokenized on non-alphanumerics and
// lowercased. Same-tool queries share the tool token, so different tools with
// the same subject never resemble each other.
func Normalize(tool, subject string) string {
	parts := strings.FieldsFunc(strings.ToLower(tool+" "+subject), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(parts, " ")
}

// LoopDetector is the degenerate-loop detector. The zero value is not ready
// to use — construct with NewLoopDetector.
type LoopDetector struct {
	mu    sync.Mutex
	seq   int // process-wide observation sequence (monotonic across sessions)
	rings map[string]*loopRing
}

// loopRing is one session key's query history: a FIFO ring (oldest first,
// capped at LoopRingSize) plus the set of earlier call sequences already
// flagged, so once-only semantics hold and the set stays bounded by the ring.
type loopRing struct {
	entries []ringEntry
	flagged map[int]struct{}
}

// ringEntry is one recorded query.
type ringEntry struct {
	CallSeq int
	Query   string
}

// NewLoopDetector returns an empty detector.
func NewLoopDetector() *LoopDetector {
	return &LoopDetector{rings: map[string]*loopRing{}}
}

// Enabled reports whether loop-suspect detection is active.
// KERN_LOOP_DETECT=0 disables it; any other value (including unset) enables
// it — the same opt-out discipline as the D1 cache and served-ledger knobs.
func (d *LoopDetector) Enabled() bool {
	return os.Getenv("KERN_LOOP_DETECT") != "0"
}

// Observe records a normalized query into sessionKey's ring and reports
// whether it resembles an earlier query in the same session. Comparison
// covers only entries >= 2 calls back (the immediately previous call is
// excluded, so a deliberate re-read right after a serve is never a loop);
// the FIRST qualifying entry wins (oldest first). Each matched earlier call
// is flagged at most once per session. The query is recorded either way.
// found=false while disabled (KERN_LOOP_DETECT=0) or when nothing qualifies.
func (d *LoopDetector) Observe(sessionKey, normalized string) (LoopSuspect, bool) {
	if !d.Enabled() {
		return LoopSuspect{}, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	seq := d.seq
	ring := d.rings[sessionKey]
	if ring == nil {
		ring = &loopRing{flagged: map[int]struct{}{}}
		d.rings[sessionKey] = ring
	}
	// Record into the ring first (either way), evicting the oldest at the cap.
	ring.entries = append(ring.entries, ringEntry{CallSeq: seq, Query: normalized})
	if len(ring.entries) > LoopRingSize {
		evicted := ring.entries[0]
		ring.entries = ring.entries[1:]
		delete(ring.flagged, evicted.CallSeq)
	}
	sig := tokenSet(normalized)
	for _, e := range ring.entries {
		if seq-e.CallSeq < 2 {
			continue // self-exclusion: only queries >= 2 calls back compare
		}
		if _, done := ring.flagged[e.CallSeq]; done {
			continue // once-only: never re-flag the same earlier query
		}
		sim := jaccard(sig, tokenSet(e.Query))
		if sim >= LoopSimThreshold {
			ring.flagged[e.CallSeq] = struct{}{}
			return LoopSuspect{CallSeq: e.CallSeq, Sim: sim, Query: e.Query}, true
		}
	}
	return LoopSuspect{}, false
}

// tokenSet returns the sorted, de-duplicated fnv32 token set of a normalized
// query (the jaccard signature; capped at loopMaxTokens). Empty for an empty
// query — jaccard returns 0 for empty signatures, so nothing flags.
func tokenSet(normalized string) []uint32 {
	words := strings.Fields(normalized)
	if len(words) == 0 {
		return nil
	}
	set := map[uint32]struct{}{}
	for _, w := range words {
		set[fnv32(w)] = struct{}{}
	}
	out := make([]uint32, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > loopMaxTokens {
		out = out[:loopMaxTokens]
	}
	return out
}
