package tokenize

// Differential tests: the batch-merge optimizations in TiktokenCounter
// .countWord and BPECounter.encodeWord must produce the exact same token
// counts as the reference one-at-a-time lowest-rank merge algorithm, on
// both the bundled fixtures and deterministic random/pathological inputs.
// The reference implementations below are copies of the pre-optimization
// algorithms.

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// refCountWord is the reference one-pair-per-scan lowest-rank merge.
func refCountWord(t *TiktokenCounter, w string) int {
	n := len(w)
	if n <= 1 {
		return n
	}
	parts := make([]int32, n+1)
	for i := range parts {
		parts[i] = int32(i)
	}
	for len(parts) > 2 {
		bestRank := int(^uint(0) >> 1)
		bestIdx := -1
		for i := 0; i+2 < len(parts); i++ {
			if r, ok := t.vocab[w[parts[i]:parts[i+2]]]; ok && r < bestRank {
				bestRank, bestIdx = r, i
			}
		}
		if bestIdx < 0 {
			break
		}
		parts = append(parts[:bestIdx+1], parts[bestIdx+2:]...)
	}
	return len(parts) - 1
}

func refCountText(t *TiktokenCounter, s string) int {
	total := 0
	if t.o200k {
		o200kWords(s, func(w string) { total += refCountWord(t, w) })
	} else {
		cl100kWords(s, func(w string) { total += refCountWord(t, w) })
	}
	return total
}

func refCount(t *TiktokenCounter, s string) int {
	if s == "" {
		return 0
	}
	total := 0
	rest := s
	for {
		idx, length := findSpecial(rest, t.specials)
		if idx < 0 {
			total += refCountText(t, rest)
			break
		}
		total += refCountText(t, rest[:idx]) + 1
		rest = rest[idx+length:]
	}
	return total
}

// refEncodeWord is the reference one-pair-per-scan BPE merge.
func refEncodeWord(b *BPECounter, w []byte) []int {
	ids := bytesToIDs(w)
	for {
		bestRank := 1 << 30
		bestPos := -1
		for i := 0; i+1 < len(ids); i++ {
			if r, ok := b.ranks[pairKey{ids[i], ids[i+1]}]; ok && r < bestRank {
				bestRank, bestPos = r, i
			}
		}
		if bestPos < 0 {
			break
		}
		merged := 256 + bestRank
		next := make([]int, 0, len(ids)-1)
		next = append(next, ids[:bestPos]...)
		next = append(next, merged)
		next = append(next, ids[bestPos+2:]...)
		ids = next
	}
	return ids
}

// randomCorpus draws deterministic pseudo-random strings spanning ordinary
// text, repeated runs, and byte-ish noise — the shapes that stress the
// merge orderings.
func randomCorpus(seed int64, n int) []string {
	r := rand.New(rand.NewSource(seed))
	alphabet := []string{
		"a", "b", "c", "x", "y", "z", "0", "1", " ", " ", ":", ".", "\t", "\n",
		"the", "and", "er", "error", "ing", "ion", "un", "de", "re", "co",
		"пак", "κέρν", "λ", "日本",
	}
	var out []string
	for i := 0; i < n; i++ {
		var sb strings.Builder
		runLen := 1 + r.Intn(40)
		for j := 0; j < runLen; j++ {
			if r.Intn(5) == 0 {
				// Repeat the previous piece to build runs / merges.
				s := alphabet[r.Intn(len(alphabet))]
				sb.WriteString(strings.Repeat(s, 1+r.Intn(6)))
			} else {
				sb.WriteString(alphabet[r.Intn(len(alphabet))])
			}
		}
		out = append(out, sb.String())
	}
	return out
}

func TestBatchMergeMatchesReferenceCl100k(t *testing.T) {
	c, err := NewCl100kCounter()
	if err != nil {
		t.Fatal(err)
	}
	inputs := append(randomCorpus(1, 400), randomCorpus(2, 400)...)
	inputs = append(inputs,
		"hello world",
		strings.Repeat("x", 3000),
		strings.Repeat("ab", 2000),
		"func CompressLog(text string) string {",
		"ERROR failed to connect to 127.0.0.1:11434",
		strings.Repeat("the ", 1500),
	)
	for _, s := range inputs {
		if got, want := c.Count(s), refCount(c, s); got != want {
			t.Fatalf("cl100k batch count %d != reference %d for %q", got, want, s[:min(len(s), 60)])
		}
	}
}

func TestBatchMergeMatchesReferenceO200k(t *testing.T) {
	c, err := NewO200kCounter()
	if err != nil {
		t.Fatal(err)
	}
	inputs := append(randomCorpus(3, 400), randomCorpus(4, 400)...)
	inputs = append(inputs,
		"hello world",
		strings.Repeat("x", 3000),
		strings.Repeat("ab", 2000),
		"func CompressLog(text string) string {",
		"ERROR failed to connect to 127.0.0.1:11434",
		strings.Repeat("the ", 1500),
	)
	for _, s := range inputs {
		if got, want := c.Count(s), refCount(c, s); got != want {
			t.Fatalf("o200k batch count %d != reference %d for %q", got, want, s[:min(len(s), 60)])
		}
	}
}

func TestBatchMergeMatchesReferenceBPE(t *testing.T) {
	b := NewBPECounter()
	inputs := append(randomCorpus(5, 400), randomCorpus(6, 400)...)
	inputs = append(inputs,
		"hello world",
		strings.Repeat("x", 3000),
		strings.Repeat("ab", 2000),
		"func CompressLog(text string) string {",
		"ERROR failed to connect to 127.0.0.1:11434",
		strings.Repeat("the ", 1500),
	)
	for _, s := range inputs {
		got := len(b.encodeWord([]byte(s)))
		want := len(refEncodeWord(b, []byte(s)))
		if got != want {
			t.Fatalf("bpe batch count %d != reference %d for %q", got, want, s[:min(len(s), 60)])
		}
	}
}

// TestLongRunCountsFast guards the regression that made the exact BPE
// counters quadratic (and effectively hang) on homogeneous runs like a
// 1M-char file: the batch merge must finish in well under the bound.
func TestLongRunCountsFast(t *testing.T) {
	c, err := NewCl100kCounter()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	n := c.Count(strings.Repeat("x", 1_000_000))
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cl100k Count(1M x) took %v, want < 10s", elapsed)
	}
	if n <= 0 {
		t.Fatalf("expected positive count, got %d", n)
	}
	if got := len(NewBPECounter().encodeWord([]byte(strings.Repeat("x", 1_000_000)))); got <= 0 {
		t.Fatalf("bpe encodeWord(1M x) = %d, want > 0", got)
	}
}
