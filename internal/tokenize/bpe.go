// Byte-level BPE tokenizer (GPT-2 style), self-contained and deterministic.
// Unlike the Estimator, BPECounter performs a real byte-pair encoding: a merge
// table is trained once from a bundled multilingual corpus (no network, no
// files), then any input is segmented by the standard lowest-rank merge
// algorithm. Counts are reproducible run-to-run because the corpus and the
// tie-breaking rule are fixed. This implements the tokenize.Counter interface
// so it can replace the estimator without changing callers.
package tokenize

import (
	"regexp"
	"sync"
)

// preTokRe approximates the GPT-2 pre-tokenizer: it splits text into
// whitespace runs, punctuation runs, letter runs and digit runs.
var preTokRe = regexp.MustCompile(`\s+|[^\p{L}\p{N}]+|\p{L}+|\p{N}+`)

// defaultMerges is the size of the trained merge table. Vocabulary = 256 base
// bytes + defaultMerges merge symbols. Kept small so startup stays instant.
const defaultMerges = 512

// bpCorpus is the bundled training corpus. It must be deterministic; the
// contents shape the vocabulary but any fixed text yields a valid tokenizer.
const bpCorpus = `Package kern is a local-only context optimizer for AI coding assistants.
It compresses prompts, logs, build output and project maps, tracks token
savings, and exposes an MCP server plus an opencode plugin.
func CompressLog(text string, opts Options) string {
    lines := strings.Split(text, "\n")
    keep := make([]string, 0, len(lines))
    for _, l := range lines {
        if shouldKeep(l) { keep = append(keep, l) }
    }
    return strings.Join(keep, "\n")
}
type Symbol struct {
    Kind     string
    Name     string
    Receiver string
    File     string
    Line     int
    Lang     string
}
The quick brown fox jumps over the lazy dog while the error handler retries
the request with a backoff of 500 milliseconds before giving up entirely.
class Shape {
    double area() const { return width * height; }
    void draw(Renderer& r) { r.paint(*this); }
}
fn main() -> Result<(), Error> {
    let store = Store::new()?;
    for line in stdin().lock().lines() { store.put(&line?); }
}
def compress_prompt(prompt, max_tokens=1024):
    tokens = tokenize(prompt)
    return tokens[:max_tokens]
const server = http.createServer((req, res) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ ok: true, saved: tokens.saved }));
});
ERROR 2026-08-04T10:15:30Z [worker-3] failed to connect to 127.0.0.1:11434
INFO 2026-08-04T10:15:31Z cache miss for internal/optimize/optimize.go
WARNING build took 42.7s, 3 tests failed out of 218
--model llama3.2 --attach build.log --session abc-123-xyz
mkdir -p bin && go build -o bin/kern ./cmd/kern && go vet ./...`

// pairKey identifies an adjacent token pair during training and encoding.
type pairKey [2]int

// BPECounter is a deterministic byte-level BPE token counter.
type BPECounter struct {
	numMerges int
	corpus    []byte
	pre       *regexp.Regexp
	ranks     map[pairKey]int
}

// NewBPECounter returns a counter trained from the bundled corpus.
func NewBPECounter() *BPECounter {
	return NewBPECounterMerges(defaultMerges)
}

// NewBPECounterMerges builds a counter with a custom merge-table size.
func NewBPECounterMerges(n int) *BPECounter {
	b := &BPECounter{
		numMerges: n,
		corpus:    []byte(bpCorpus),
		pre:       preTokRe,
	}
	b.train()
	return b
}

// train learns the merge table from the corpus and builds the rank index.
func (b *BPECounter) train() {
	words := b.pre.FindAll(b.corpus, -1)
	ids := make([][]int, len(words))
	for i, w := range words {
		ids[i] = bytesToIDs(w)
	}
	merges := trainMerges(ids, b.numMerges)
	b.ranks = make(map[pairKey]int, len(merges))
	for i, p := range merges {
		b.ranks[pairKey{p[0], p[1]}] = i
	}
}

// Count returns the number of BPE tokens in s.
func (b *BPECounter) Count(s string) int {
	if s == "" {
		return 0
	}
	toks := b.pre.FindAll([]byte(s), -1)
	n := 0
	for _, t := range toks {
		n += len(b.encodeWord(t))
	}
	return n
}

// encodeWord merges a single pre-token by lowest-rank pair until fixed point.
func (b *BPECounter) encodeWord(w []byte) []int {
	ids := bytesToIDs(w)
	for {
		bestRank := 1 << 30
		bestPos := -1
		for i := 0; i+1 < len(ids); i++ {
			if r, ok := b.ranks[pairKey{ids[i], ids[i+1]}]; ok && r < bestRank {
				bestRank = r
				bestPos = i
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

func bytesToIDs(b []byte) []int {
	ids := make([]int, len(b))
	for i, c := range b {
		ids[i] = int(c)
	}
	return ids
}

// trainMerges greedily merges the most frequent adjacent pair numMerges times.
// Ties are broken lexicographically so the result is independent of map order.
func trainMerges(words [][]int, numMerges int) [][2]int {
	cur := make([][]int, len(words))
	copy(cur, words)
	var merges [][2]int
	for m := 0; m < numMerges; m++ {
		counts := make(map[pairKey]int)
		for _, w := range cur {
			for i := 0; i+1 < len(w); i++ {
				counts[pairKey{w[i], w[i+1]}]++
			}
		}
		var best pairKey
		bestCnt := 0
		for k, c := range counts {
			if c > bestCnt || (c == bestCnt && (k[0] < best[0] || (k[0] == best[0] && k[1] < best[1]))) {
				best = k
				bestCnt = c
			}
		}
		if bestCnt == 0 {
			break
		}
		merges = append(merges, [2]int{best[0], best[1]})
		merged := 256 + m
		for wi, w := range cur {
			cur[wi] = applyMerge(w, best, merged)
		}
	}
	return merges
}

// applyMerge replaces every adjacent occurrence of pair with the new id.
func applyMerge(ids []int, pair pairKey, newID int) []int {
	out := make([]int, 0, len(ids))
	for i := 0; i < len(ids); {
		if i+1 < len(ids) && ids[i] == pair[0] && ids[i+1] == pair[1] {
			out = append(out, newID)
			i += 2
		} else {
			out = append(out, ids[i])
			i++
		}
	}
	return out
}

// DefaultBPE is a lazily-initialized counter shared by callers.
var DefaultBPE = sync.OnceValue(NewBPECounter)

// CountBPE is a convenience wrapper around the default BPE counter.
func CountBPE(s string) int {
	return DefaultBPE().Count(s)
}

// Compile-time check that BPECounter satisfies the Counter interface.
var _ Counter = (*BPECounter)(nil)
