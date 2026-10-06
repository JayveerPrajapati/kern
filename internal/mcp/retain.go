package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// --- Retained-output cursor (R7 "more") ---
//
// When the MCP output sandbox truncates a response, the FULL pre-truncation
// text is kept in a small bounded in-memory store under a short anchor id
// ("anchor-<hex>", the kern_verify anchor style) and the sandbox marker
// advertises slice=<anchor>:lines:A-B|tail:N. A caller re-invoking the same
// tool with slice= gets ONLY that slice of the retained text, served by
// runTool's short-circuit (server.go) WITHOUT re-executing the tool —
// intercepted before the D1 cache gate and dispatch.
//
// The store is deliberately SEPARATE from the D1 tool cache (tool_cache.go),
// even though that cache also keeps pre-sandbox raw text: the D1 cache only
// holds Cacheable tools (an allowlisted subset), is keyed by the call's full
// argument set (a slice re-call carries different args, so it cannot look the
// entry up), persists to disk with a 24h TTL and can be disabled wholesale
// (KERN_MCP_CACHE=0). The cursor must work for EVERY tool (the sandbox
// applies to all), must be findable by anchor alone, and must stay ephemeral
// — hence its own store.
//
// Bounds mirror the D1 cache and kern_verify conventions: an LRU capped at
// retainedOutputLRUCap entries and retainedOutputMaxBytes total, plus a TTL
// (retainedOutputTTL; KERN_MCP_RETAIN_TTL env override with the
// KERN_MCP_CACHE_TTL convention, default 10m, 0 = no expiry). Outputs larger
// than the whole byte budget are not retained at all (retainOutput returns
// ""), mirroring toolCacheEntrySizeCap's "a huge dump costs more to keep than
// to recompute" rule (F9). The store is per-server-process and in-memory
// only: an anchor dies with the server, like optimize's in-memory anchor
// store (the kern_verify cursor precedent).

const (
	// retainedOutputLRUCap bounds the cursor store's entry count.
	retainedOutputLRUCap = 16
	// retainedOutputMaxBytes bounds the cursor store's total retained bytes
	// AND the largest single text that will be retained at all.
	retainedOutputMaxBytes = 1 << 20 // 1 MiB
	// retainedOutputDefaultTTL is how long a cursor stays sliceable.
	retainedOutputDefaultTTL = 10 * time.Minute
)

type retainedEntry struct {
	text string
	ts   time.Time
}

var (
	retainMu    sync.Mutex
	retainOrder []string // most-recently-used first
	retainItems = map[string]retainedEntry{}
	retainBytes int
)

// retainedOutputTTL returns the cursor TTL from KERN_MCP_RETAIN_TTL. Default
// 10m; 0 means no expiry. An unparsable value warns on stderr instead of
// silently falling back (R6, fails-loud) — the same contract as cacheTTL.
func retainedOutputTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("KERN_MCP_RETAIN_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "kern-mcp: KERN_MCP_RETAIN_TTL=%q not a duration, using 10m\n", v)
	}
	return retainedOutputDefaultTTL
}

// retainOutput stores text under a deterministic content-derived anchor id
// ("anchor-" + 12 hex of the text's sha256, the optimize.StoreAnchor style —
// re-truncating identical text refreshes the SAME entry instead of
// accumulating duplicates) and returns the id. It returns "" when the text
// is empty or too large to retain (larger than the whole byte budget). The
// store is bounded: inserting past retainedOutputLRUCap entries or
// retainedOutputMaxBytes evicts the LRU tail (the just-inserted entry is
// never evicted). Deterministic anchors mean two tools truncating identical
// text share one entry — harmless, since a slice returns only the text.
func retainOutput(text string) string {
	if text == "" || len(text) > retainedOutputMaxBytes {
		return ""
	}
	h := sha256.Sum256([]byte(text))
	id := "anchor-" + hex.EncodeToString(h[:])[:12]
	retainMu.Lock()
	defer retainMu.Unlock()
	if _, ok := retainItems[id]; !ok {
		retainBytes += len(text)
	}
	retainItems[id] = retainedEntry{text: text, ts: time.Now()}
	retainPromoteLocked(id)
	// Evict the LRU tail while over either bound. The admission gate above
	// keeps a single entry within the byte budget; the len(retainOrder) > 1
	// guard still protects the newest entry from being the only victim.
	for (len(retainOrder) > retainedOutputLRUCap || retainBytes > retainedOutputMaxBytes) && len(retainOrder) > 1 {
		tail := retainOrder[len(retainOrder)-1]
		retainOrder = retainOrder[:len(retainOrder)-1]
		if e, ok := retainItems[tail]; ok {
			retainBytes -= len(e.text)
			delete(retainItems, tail)
		}
	}
	return id
}

// fetchRetainedOutput returns the retained entry for id, promoting it to MRU.
// A missing or expired id is an error that names BOTH the anchor and the
// expiry so the caller knows to re-run the tool — the marker's slice= advice
// can only be honored while the cursor lives.
func fetchRetainedOutput(id string) (retainedEntry, error) {
	retainMu.Lock()
	defer retainMu.Unlock()
	e, ok := retainItems[id]
	if !ok {
		return retainedEntry{}, fmt.Errorf("slice: anchor %s not found or expired (retained output TTL %s; re-run the tool to mint a fresh anchor)", id, retainedOutputTTL())
	}
	if ttl := retainedOutputTTL(); ttl > 0 && time.Since(e.ts) > ttl {
		delete(retainItems, id)
		retainBytes -= len(e.text)
		for i, k := range retainOrder {
			if k == id {
				retainOrder = append(retainOrder[:i], retainOrder[i+1:]...)
				break
			}
		}
		return retainedEntry{}, fmt.Errorf("slice: anchor %s expired (retained output TTL %s; re-run the tool to mint a fresh anchor)", id, retainedOutputTTL())
	}
	retainPromoteLocked(id)
	return e, nil
}

// retainPromoteLocked moves id to the MRU front. Caller holds retainMu.
func retainPromoteLocked(id string) {
	for i, k := range retainOrder {
		if k == id {
			retainOrder = append(retainOrder[:i], retainOrder[i+1:]...)
			break
		}
	}
	retainOrder = append([]string{id}, retainOrder...)
}

// retainReset clears the cursor store. Test-only: keeps cursor tests
// deterministic across cases sharing the package-global store.
func retainReset() {
	retainMu.Lock()
	defer retainMu.Unlock()
	retainOrder = nil
	retainItems = map[string]retainedEntry{}
	retainBytes = 0
}

// serveRetainedSlice handles a slice=<anchor>:lines:A-B|tail:N re-call at the
// runTool short-circuit (server.go): it resolves the anchor in the
// retained-output store and returns ONLY the requested lines of the retained
// full text. The tool is NOT executed — a slice re-call carries no tool
// arguments, so execution would fail on the tool's missing required args; the
// intercept exists precisely to serve the elided remainder without re-running
// (R7). An invalid, expired or out-of-range slice is an error naming the
// anchor and the expiry/range.
func serveRetainedSlice(spec string) (string, error) {
	anchor, mode, err := parseSliceSpec(spec)
	if err != nil {
		return "", err
	}
	e, err := fetchRetainedOutput(anchor)
	if err != nil {
		return "", err
	}
	return sliceRetainedLines(e.text, mode)
}

// parseSliceSpec parses the slice cursor grammar:
//
//	slice=<anchor>:lines:A-B   — lines A..B (1-based) of the retained output
//	slice=<anchor>:tail:N      — the last N lines of the retained output
//
// <anchor> is the id from the sandbox marker ("anchor-" + 12 hex). Anything
// that does not match the grammar is an error naming the grammar.
func parseSliceSpec(spec string) (anchor, mode string, err error) {
	i := strings.IndexByte(spec, ':')
	if i <= 0 {
		return "", "", fmt.Errorf("slice: expected <anchor>:lines:A-B or <anchor>:tail:N, got %q", spec)
	}
	anchor, mode = spec[:i], spec[i+1:]
	if !validAnchor(anchor) {
		return "", "", fmt.Errorf("slice: invalid anchor %q (expected anchor-<12 hex> from the sandbox marker)", anchor)
	}
	return anchor, mode, nil
}

// validAnchor reports whether id matches the retained-cursor anchor format:
// the "anchor-" prefix plus 12 lowercase hex digits (the optimize.StoreAnchor
// shape this store mints). Strictness here keeps a typo'd slice= from
// reaching the store with a garbled id.
func validAnchor(id string) bool {
	if !strings.HasPrefix(id, "anchor-") || len(id) != len("anchor-")+12 {
		return false
	}
	for _, c := range id[len("anchor-"):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// sliceRetainedLines returns the requested slice of the retained text. Line
// numbers are 1-based and every returned line is prefixed "N: " (the
// verifycmd re-slice convention) so the caller can navigate onward slices.
// tail:N clamps to the available lines; lines:A-B errors when A is past the
// end and clamps B to the end (the verifycmd shapeOutput behavior).
func sliceRetainedLines(text, mode string) (string, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	switch {
	case strings.HasPrefix(mode, "tail:"):
		n, err := strconv.Atoi(strings.TrimPrefix(mode, "tail:"))
		if err != nil || n < 1 {
			return "", fmt.Errorf("slice: tail:N needs a positive number, got %q", mode)
		}
		if n > len(lines) {
			n = len(lines)
		}
		return numberedLines(lines, len(lines)-n+1, len(lines)), nil
	case strings.HasPrefix(mode, "lines:"):
		a, b, err := parseRetainedRange(strings.TrimPrefix(mode, "lines:"))
		if err != nil {
			return "", err
		}
		if a > len(lines) {
			return "", fmt.Errorf("slice: lines:%d-%d is past the end (%d lines; re-run the tool or slice a lower range)", a, b, len(lines))
		}
		if b > len(lines) {
			b = len(lines)
		}
		return numberedLines(lines, a, b), nil
	}
	return "", fmt.Errorf("slice: unknown mode %q (use lines:A-B or tail:N)", mode)
}

// parseRetainedRange parses "A-B" (both 1-based, A <= B) for lines:A-B.
func parseRetainedRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("slice: lines:A-B needs a range like lines:1-50, got %q", s)
	}
	a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || a < 1 {
		return 0, 0, fmt.Errorf("slice: lines:A-B needs positive numbers, got %q", s)
	}
	b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || b < a {
		return 0, 0, fmt.Errorf("slice: lines:A-B needs A <= B, got %q", s)
	}
	return a, b, nil
}

// numberedLines renders lines[from-1:to] with "N: " prefixes.
func numberedLines(lines []string, from, to int) string {
	if from > to || from < 1 {
		return ""
	}
	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i, lines[i-1])
	}
	return strings.TrimSuffix(b.String(), "\n")
}
