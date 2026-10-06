package mcp

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRetainedOutputStoreAndSlice covers the R7 cursor store round trip: a
// retained output is fetchable by its anchor and slices exactly the requested
// lines (verifycmd re-slice conventions: 1-based, "N: " prefixed, tail and
// range-end clamping, past-the-end errors).
func TestRetainedOutputStoreAndSlice(t *testing.T) {
	retainReset()
	defer retainReset()

	text := "line1\nline2\nline3\nline4\nline5\n"
	anchor := retainOutput(text)
	if !strings.HasPrefix(anchor, "anchor-") {
		t.Fatalf("anchor format: %q", anchor)
	}
	if e, err := fetchRetainedOutput(anchor); err != nil || e.text != text {
		t.Fatalf("fetch round trip: err=%v", err)
	}

	cases := []struct {
		spec string
		want string
	}{
		{anchor + ":lines:2-4", "2: line2\n3: line3\n4: line4"},
		{anchor + ":tail:2", "4: line4\n5: line5"},
		{anchor + ":tail:999", "1: line1\n2: line2\n3: line3\n4: line4\n5: line5"}, // clamped
		{anchor + ":lines:3-99", "3: line3\n4: line4\n5: line5"},                   // B clamped
		{anchor + ":lines:5-5", "5: line5"},
	}
	for _, c := range cases {
		got, err := serveRetainedSlice(c.spec)
		if err != nil {
			t.Fatalf("slice %q: unexpected error %v", c.spec, err)
		}
		if got != c.want {
			t.Fatalf("slice %q = %q, want %q", c.spec, got, c.want)
		}
	}

	// A start past the end errors.
	if _, err := serveRetainedSlice(anchor + ":lines:9-10"); err == nil || !strings.Contains(err.Error(), "past the end") {
		t.Fatalf("expected a past-the-end error, got %v", err)
	}
	// Malformed grammar errors naming the grammar.
	for _, bad := range []string{
		"", "anchor-abc", "anchor-0123456789ab", "anchor-0123456789ab:tail:0",
		"anchor-0123456789ab:tail:junk", "anchor-0123456789ab:lines:2",
		"anchor-0123456789ab:lines:4-2", "anchor-0123456789ab:lines:0-2",
		"anchor-0123456789ab:middle:1-2", "anchor-0123456789ab:",
	} {
		if _, err := serveRetainedSlice(bad); err == nil {
			t.Fatalf("expected an error for slice=%q", bad)
		}
	}
}

// TestRetainedOutputAnchorErrors: a missing or expired anchor is a clear
// error naming BOTH the anchor and the TTL, so the caller knows to re-run the
// tool (the marker's slice= advice is only good while the cursor lives).
func TestRetainedOutputAnchorErrors(t *testing.T) {
	retainReset()
	defer retainReset()

	_, err := fetchRetainedOutput("anchor-000000000000")
	if err == nil {
		t.Fatal("expected an error for a missing anchor")
	}
	for _, frag := range []string{"anchor-000000000000", "TTL"} {
		if !strings.Contains(err.Error(), frag) {
			t.Fatalf("missing-anchor error must name %q: %v", frag, err)
		}
	}

	anchor := retainOutput("fresh\n")
	retainMu.Lock()
	e := retainItems[anchor]
	e.ts = time.Now().Add(-2 * retainedOutputDefaultTTL)
	retainItems[anchor] = e
	retainMu.Unlock()
	_, err = fetchRetainedOutput(anchor)
	if err == nil || !strings.Contains(err.Error(), "expired") || !strings.Contains(err.Error(), anchor) {
		t.Fatalf("expired-anchor error must name the anchor and expiry, got %v", err)
	}
	// The expired entry is gone from the store.
	if _, ok := retainItems[anchor]; ok {
		t.Fatal("expired entry must be removed from the store")
	}
}

// TestRetainedOutputBounds exercises both bounds plus the oversized/empty
// admission gates: 17 distinct inserts evict the 1st (LRU cap 16); a re-fetch
// promotes an entry and protects it from the next eviction; the byte cap keeps
// the aggregate under retainedOutputMaxBytes; texts larger than the whole
// byte budget (or empty) are never retained.
func TestRetainedOutputBounds(t *testing.T) {
	retainReset()
	defer retainReset()

	// LRU cap: the 1st entry is evicted after 16 more distinct inserts.
	first := retainOutput("entry-000000")
	for i := 1; i < retainedOutputLRUCap+1; i++ {
		retainOutput("entry-" + strconv.Itoa(i))
	}
	if _, err := fetchRetainedOutput(first); err == nil {
		t.Fatal("LRU cap: the 1st entry must be evicted after 16 more inserts")
	}

	// MRU promotion protects an entry from eviction.
	a := retainOutput("protect-me\n")
	for i := 0; i < retainedOutputLRUCap-1; i++ {
		retainOutput("filler-" + strconv.Itoa(i))
	}
	if _, err := fetchRetainedOutput(a); err != nil {
		t.Fatalf("fetch before the evicting insert: %v", err)
	}
	retainOutput("filler-16")
	if _, err := fetchRetainedOutput(a); err != nil {
		t.Fatal("MRU promotion must protect an entry from the next eviction")
	}

	// Byte cap: 24 x 100 KiB inserts keep the aggregate under 1 MiB (and the
	// entry count under the LRU cap).
	retainReset()
	for i := 0; i < 24; i++ {
		retainOutput(strings.Repeat("z", 100<<10) + "-" + strconv.Itoa(i))
	}
	retainMu.Lock()
	total, n := retainBytes, len(retainItems)
	retainMu.Unlock()
	if total > retainedOutputMaxBytes {
		t.Fatalf("byte cap exceeded: retained %d > %d", total, retainedOutputMaxBytes)
	}
	if n > retainedOutputLRUCap {
		t.Fatalf("entry cap exceeded: %d entries", n)
	}

	// Oversized and empty outputs are not retained at all.
	if id := retainOutput(strings.Repeat("q", retainedOutputMaxBytes+1)); id != "" {
		t.Fatal("outputs larger than the whole byte budget must not be retained")
	}
	if id := retainOutput(""); id != "" {
		t.Fatal("empty output must not be retained")
	}
}

// TestRetainedOutputDeterministicAnchor: identical text mints the same anchor
// (the optimize.StoreAnchor style), so re-truncating identical output
// refreshes one entry instead of accumulating duplicates.
func TestRetainedOutputDeterministicAnchor(t *testing.T) {
	retainReset()
	defer retainReset()

	id1 := retainOutput("same\n")
	id2 := retainOutput("same\n")
	if id1 != id2 {
		t.Fatalf("identical text must mint the same anchor: %q vs %q", id1, id2)
	}
	if _, err := fetchRetainedOutput(id1); err != nil {
		t.Fatalf("refresh must keep the entry fetchable: %v", err)
	}
}

// TestRetainedOutputTTLEnv pins the KERN_MCP_RETAIN_TTL knob (the
// KERN_MCP_CACHE_TTL convention): a valid duration applies, an invalid value
// falls back to the default with a stderr warning.
func TestRetainedOutputTTLEnv(t *testing.T) {
	t.Setenv("KERN_MCP_RETAIN_TTL", "5m")
	if d := retainedOutputTTL(); d != 5*time.Minute {
		t.Fatalf("TTL = %v, want 5m", d)
	}
	t.Setenv("KERN_MCP_RETAIN_TTL", "0")
	if d := retainedOutputTTL(); d != 0 {
		t.Fatalf("TTL 0 must mean no expiry, got %v", d)
	}
	t.Setenv("KERN_MCP_RETAIN_TTL", "junk")
	if d := retainedOutputTTL(); d != retainedOutputDefaultTTL {
		t.Fatalf("invalid TTL should fall back to the default, got %v", d)
	}
}
