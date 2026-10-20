package semcache

import (
	"fmt"
	"strings"
	"testing"
)

// tokN builds a normalized query with exactly n tokens: the tool token plus
// n-1 distinct words ("wNN"). Distinct queries never overlap beyond the tool
// token, so tests control the token counts exactly.
func tokN(prefix string, n int) string {
	words := make([]string, 0, n)
	words = append(words, prefix)
	for i := 0; i < n-1; i++ {
		words = append(words, fmt.Sprintf("w%02d", i))
	}
	return strings.Join(words, " ")
}

// TestNormalize pins the query normalization: tool + subject, tokenized on
// non-alphanumerics (the tool name's underscore splits like any other
// separator), lowercased.
func TestNormalize(t *testing.T) {
	got := Normalize("kern_search", "How-Does.Dispatch_Work?!")
	if got != "kern search how does dispatch work" {
		t.Fatalf("Normalize = %q, want %q", got, "kern search how does dispatch work")
	}
}

// TestLoopDetectorThresholdBoundary: similarity just below 0.80 does not
// flag; just above does. The compare must run against an entry >= 2 calls
// back, so a distinct buffer query occupies call #2.
func TestLoopDetectorThresholdBoundary(t *testing.T) {
	t.Run("below", func(t *testing.T) {
		d := NewLoopDetector()
		// 24-token earlier query; new query shares the first 19 → 19/24 =
		// 0.7917 < 0.80 → no flag.
		d.Observe("s", tokN("kern_search", 24))                   // call #1
		d.Observe("s", "kern_search x1 y1 z1")                    // call #2 (buffer)
		if s, ok := d.Observe("s", tokN("kern_search", 19)); ok { // call #3
			t.Fatalf("sim 19/24 = 0.7917 must not flag, got suspect %+v", s)
		}
	})
	t.Run("at-and-above", func(t *testing.T) {
		d := NewLoopDetector()
		// 21-token earlier query; new query shares the first 17 → 17/21 =
		// 0.8095 >= 0.80 → flags the earlier call.
		d.Observe("s", tokN("kern_search", 21))          // call #1
		d.Observe("s", "kern_search x1 y1 z1")           // call #2 (buffer)
		s, ok := d.Observe("s", tokN("kern_search", 17)) // call #3
		if !ok {
			t.Fatal("sim 17/21 = 0.8095 must flag")
		}
		if s.CallSeq != 1 {
			t.Fatalf("suspect must reference call #1, got #%d", s.CallSeq)
		}
		if s.Sim <= 0.80 || s.Sim >= 0.81 {
			t.Fatalf("suspect sim = %v, want ~0.8095", s.Sim)
		}
		if s.Query != tokN("kern_search", 21) {
			t.Fatalf("suspect must carry the earlier normalized query, got %q", s.Query)
		}
	})
}

// TestLoopDetectorRingCapEviction: at the 32-entry cap the oldest entry is
// evicted — a query identical to it no longer flags — while live entries
// still flag.
func TestLoopDetectorRingCapEviction(t *testing.T) {
	d := NewLoopDetector()
	// Fill to the cap with fully distinct queries (unique tokens per call,
	// so nothing flags during the fill).
	for i := 0; i < LoopRingSize; i++ {
		d.Observe("s", fmt.Sprintf("kern_search u%02d v%02d w%02d x%02d y%02d z%02d", i, i, i, i, i, i))
	}
	// Call #33 evicts call #1 (u00...).
	d.Observe("s", "kern_search filler a b c d e")
	// Call #34 = call #1's text: evicted → no entry resembles it.
	if _, ok := d.Observe("s", "kern_search u00 v00 w00 x00 y00 z00"); ok {
		t.Fatal("an evicted ring entry must not flag")
	}
	// Call #35 = call #32's text (still in the ring, 3 calls back): flags.
	s, ok := d.Observe("s", "kern_search u31 v31 w31 x31 y31 z31")
	if !ok {
		t.Fatal("a live ring entry must still flag")
	}
	if s.CallSeq != 32 {
		t.Fatalf("suspect must reference call #32, got #%d", s.CallSeq)
	}
}

// TestLoopDetectorSelfExclusion: the immediately previous call (1 call back)
// is never a loop — an exact repeat right after a serve does not flag —
// while a near-duplicate 2+ calls back does.
func TestLoopDetectorSelfExclusion(t *testing.T) {
	d := NewLoopDetector()
	d.Observe("s", "kern_search foo bar baz")                   // call #1
	if _, ok := d.Observe("s", "kern_search foo bar baz"); ok { // call #2 (1 back)
		t.Fatal("an immediate repeat (1 call back) must not flag")
	}
	// Call #3 is a near-duplicate of call #1 (2 back): 4/5 = 0.80 → flags.
	s, ok := d.Observe("s", "kern_search foo bar baz qux")
	if !ok {
		t.Fatal("a near-duplicate 2 calls back must flag")
	}
	if s.CallSeq != 1 {
		t.Fatalf("suspect must reference call #1, got #%d", s.CallSeq)
	}
}

// TestLoopDetectorOnceOnly: a given earlier call is flagged at most once per
// session — the marker never nags on every subsequent near-duplicate — while
// a different, not-yet-flagged earlier call still flags.
func TestLoopDetectorOnceOnly(t *testing.T) {
	d := NewLoopDetector()
	d.Observe("s", "kern_search a1 a2 a3 a4 a5 a6 a7") // call #1
	d.Observe("s", "kern_search b1 b2 b3 b4 b5 b6")    // call #2 (buffer, distinct)
	// Call #3: near-duplicate of #1 (9 vs 8 tokens → 8/9 = 0.889) → flags #1.
	s3, ok := d.Observe("s", "kern_search a1 a2 a3 a4 a5 a6 a7 a8")
	if !ok || s3.CallSeq != 1 {
		t.Fatalf("first near-duplicate must flag call #1, got %+v ok=%v", s3, ok)
	}
	// Call #4: another near-duplicate of #1 (8/9 = 0.889) → #1 already
	// flagged → must NOT flag.
	if _, ok := d.Observe("s", "kern_search a1 a2 a3 a4 a5 a6 a7 a9"); ok {
		t.Fatal("an already-flagged earlier call must not re-flag")
	}
	// Call #5: near-duplicate of #2 (7/8 = 0.875) → #2 not yet flagged → flags.
	s5, ok := d.Observe("s", "kern_search b1 b2 b3 b4 b5 b6 b7")
	if !ok || s5.CallSeq != 2 {
		t.Fatalf("a not-yet-flagged earlier call must still flag, got %+v ok=%v", s5, ok)
	}
}

// TestLoopDetectorDisabledEnv: KERN_LOOP_DETECT=0 short-circuits Observe —
// found=false and nothing is recorded, so re-enabling later sees an empty
// ring.
func TestLoopDetectorDisabledEnv(t *testing.T) {
	t.Setenv("KERN_LOOP_DETECT", "0")
	d := NewLoopDetector()
	if d.Enabled() {
		t.Fatal("KERN_LOOP_DETECT=0 must disable the detector")
	}
	d.Observe("s", "kern_search a b c d e f") // nothing recorded
	if _, ok := d.Observe("s", "kern_search a b c d e f g"); ok {
		t.Fatal("disabled detector must return found=false")
	}
	// Re-enable: the disabled-era queries were never recorded, so a repeat of
	// one cannot flag.
	t.Setenv("KERN_LOOP_DETECT", "1")
	if !d.Enabled() {
		t.Fatal("unset/1 must enable the detector (default ON)")
	}
	if _, ok := d.Observe("s", "kern_search a b c d e f g"); ok {
		t.Fatal("queries observed while disabled must not be in the ring")
	}
}
