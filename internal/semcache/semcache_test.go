package semcache

import (
	"strconv"
	"strings"
	"testing"
)

func TestSimilarity(t *testing.T) {
	if got := Similarity("hello world", "hello world"); got != 1 {
		t.Fatalf("identical should be 1, got %v", got)
	}
	if got := Similarity("", "anything"); got != 0 {
		t.Fatalf("empty should be 0, got %v", got)
	}
	// Near-duplicate phrasing shares most shingles.
	a := "the database connection failed during migration"
	b := "the database connection failed during the migration run"
	if got := Similarity(a, b); got < 0.5 {
		t.Fatalf("near-duplicate should be similar, got %v", got)
	}
	// Unrelated sentences are disjoint.
	c := "buy cheap luxury apartments in zurich"
	if got := Similarity(a, c); got > 0.3 {
		t.Fatalf("unrelated should be dissimilar, got %v", got)
	}
	// Word order does not matter (the whole point of semantic overlap).
	d := "during migration the database connection failed"
	if got := Similarity(a, d); got < 0.5 {
		t.Fatalf("reordered sentence should stay similar, got %v", got)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	_ = Store("prompt", "how do I compress a large server log?", "cached: dedupe timestamps")
	var v string
	matched, sim, hit, err := Lookup("prompt", "how do I compress a big server log", &v, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatalf("expected a fuzzy hit, sim=%v", sim)
	}
	if v != "cached: dedupe timestamps" {
		t.Fatalf("wrong payload %q", v)
	}
	if matched == "" {
		t.Fatalf("expected matched input reported")
	}
	if sim < 0.5 {
		t.Fatalf("expected meaningful similarity, got %v", sim)
	}

	// Disjoint input must NOT hit, even though the cache is fuzzy.
	_ = Store("prompt", "how do I compress a large server log?", "x")
	var v2 string
	_, _, hit, err = Lookup("prompt", "buy a ticket to the opera", &v2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatalf("disjoint input should not hit")
	}

	// Identical input always hits with similarity 1.
	_, sim, hit, err = Lookup("prompt", "how do I compress a large server log?", &v2, 0)
	if err != nil || !hit || sim != 1 {
		t.Fatalf("identical input should hit with sim=1, hit=%v sim=%v err=%v", hit, sim, err)
	}
}

func TestNamespacesAreSeparate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	_ = Store("prompt", "panic in handler at line 42", "prompt-result")
	_ = Store("log", "panic in handler at line 42", "log-result")
	var v string
	if _, _, hit, _ := Lookup("prompt", "panic in handler line 42", &v, 0); !hit || v != "prompt-result" {
		t.Fatalf("prompt ns wrong: hit=%v v=%q", hit, v)
	}
	if _, _, hit, _ := Lookup("log", "panic in handler line 42", &v, 0); !hit || v != "log-result" {
		t.Fatalf("log ns wrong: hit=%v v=%q", hit, v)
	}
}

func TestIndexBoundedAndClearable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	for i := 0; i < MaxEntries+50; i++ {
		_ = Store("bench", "input number "+string(rune('a'+i%26))+" with words "+string(rune('z'-i%26)), "p")
	}
	ents, err := Entries("bench")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) > MaxEntries {
		t.Fatalf("index not bounded: %d entries", len(ents))
	}
	stats, err := Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["bench"] != len(ents) {
		t.Fatalf("stats mismatch: %v vs %d", stats["bench"], len(ents))
	}
	if err := Clear("bench"); err != nil {
		t.Fatal(err)
	}
	if n, _ := Entries("bench"); len(n) != 0 {
		t.Fatalf("clear left %d entries", len(n))
	}
}

func TestSignatureStable(t *testing.T) {
	words := make([]string, 3000)
	for i := range words {
		words[i] = "word" + strconv.Itoa(i)
	}
	big := strings.Join(words, " ")
	sa := shingles(big)
	sb := shingles(big)
	if len(sa) != len(sb) {
		t.Fatalf("signatures unstable: %d vs %d", len(sa), len(sb))
	}
	if len(sa) != MaxShingles {
		t.Fatalf("signature not capped: %d", len(sa))
	}
}
