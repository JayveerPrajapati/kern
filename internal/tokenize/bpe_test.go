package tokenize

import "testing"

func TestBPECounterSatisfiesInterface(t *testing.T) {
	var c Counter = NewBPECounter()
	if c.Count("") != 0 {
		t.Fatal("empty string must count 0")
	}
}

func TestBPEDeterministic(t *testing.T) {
	b := NewBPECounter()
	for _, s := range []string{
		"hello world",
		"func CompressLog(text string) string {",
		"ERROR failed to connect to 127.0.0.1:11434",
		"kern optimize --llm llama3.2 --attach build.log",
		"κερν τοκενς",
	} {
		first := b.Count(s)
		for i := 0; i < 5; i++ {
			if b.Count(s) != first {
				t.Fatalf("count unstable for %q: %d vs %d", s, first, b.Count(s))
			}
		}
	}
}

func TestBPECountBPE(t *testing.T) {
	if CountBPE("") != 0 {
		t.Fatal("CountBPE empty must be 0")
	}
	if CountBPE("a") != 1 {
		t.Fatalf("CountBPE(\"a\") = %d, want 1", CountBPE("a"))
	}
}

func TestTrainMergesKnownCorpus(t *testing.T) {
	// Corpus "ab ab ab": (a,b) occurs 3 times, every other pair twice, so the
	// first merge must be (a,b).
	b := &BPECounter{
		numMerges: 8,
		corpus:    []byte("ab ab ab"),
		pre:       preTokRe,
	}
	b.train()
	if r, ok := b.ranks[pairKey{'a', 'b'}]; !ok || r != 0 {
		t.Fatalf("expected (a,b) as merge rank 0, got ok=%v rank=%d", ok, r)
	}

	// "abab" pre-tokenizes to one word [a b a b]; merging (a,b) twice yields
	// exactly two tokens.
	if n := b.Count("abab"); n != 2 {
		t.Fatalf("Count(\"abab\") = %d, want 2", n)
	}
}

func TestTrainMergesDeterministicTieBreak(t *testing.T) {
	// Corpus with equal pair frequencies: both "xy" and "yz" appear twice.
	b := &BPECounter{
		numMerges: 8,
		corpus:    []byte("xyxy yzyz"),
		pre:       preTokRe,
	}
	b.train()
	// The tie between equally-frequent pairs must resolve identically across
	// fresh instances (map iteration is randomized, so this guards order
	// independence).
	other := &BPECounter{
		numMerges: 8,
		corpus:    []byte("xyxy yzyz"),
		pre:       preTokRe,
	}
	other.train()
	if len(b.ranks) != len(other.ranks) {
		t.Fatal("merge table size differs between identical instances")
	}
	for k, r := range b.ranks {
		if other.ranks[k] != r {
			t.Fatalf("rank mismatch for %v: %d vs %d", k, r, other.ranks[k])
		}
	}
}

func TestBPEGrowsWithInput(t *testing.T) {
	b := NewBPECounter()
	short := b.Count("error handler retries the request")
	long := b.Count("error handler retries the request and then the fallback handler retries the request again")
	if long <= short {
		t.Fatalf("longer input should not count fewer tokens: short=%d long=%d", short, long)
	}
}
