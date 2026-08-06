// Package docsearch chunks local documents and indexes them with a
// deterministic, dependency-free vector embedding (feature-hashed character
// n-grams). Queries are embedded the same way and matched by cosine
// similarity, so only the most relevant fragments get surfaced to the agent.
// Everything runs locally — no ML models, no network, no external libraries.
package docsearch

import (
	"hash/fnv"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// Chunk is one contiguous fragment of a source document.
type Chunk struct {
	File  string `json:"file"`
	Start int    `json:"start"` // 1-based first line of the chunk
	Text  string `json:"text"`
}

// Doc is an indexed chunk with its embedding.
type Doc struct {
	ID   string          `json:"id"`
	Chunk Chunk          `json:"chunk"`
	Vec  map[int]float64 `json:"vec"`
}

// Index is a set of embedded chunks for one root.
type Index struct {
	Root string `json:"root"`
	Docs []Doc  `json:"docs"`
}

// Score ranks a chunk against a query.
type Score struct {
	Doc  Doc
	Sim  float64
}

var extRe = regexp.MustCompile(`\.(md|markdown|txt|rst|adoc|asciidoc|org)$`)

// CacheKey returns the persisted-index cache key for root.
func CacheKey(root string) string {
	abs, _ := filepath.Abs(root)
	return "docs/" + cache.Hash([]byte(abs))
}

// Load reads a previously persisted index for root. Returns nil when absent.
func Load(root string) *Index {
	ix := &Index{}
	if err := cache.Load(CacheKey(root), ix); err != nil {
		return nil
	}
	return ix
}

// IndexDir walks root and chunks + embeds every document file. It returns the
// in-memory index (callers may persist it with Save).
func IndexDir(root string) (*Index, error) {
	ix := &Index{Root: root}
	seen := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !extRe.MatchString(strings.ToLower(d.Name())) {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, c := range ChunkText(string(b), 2000) {
			key := rel + "#" + itoa(c.Start)
			ix.Docs = append(ix.Docs, Doc{
				ID:    key,
				Chunk: Chunk{File: rel, Start: c.Start, Text: c.Text},
				Vec:   Embed(c.Text),
			})
			seen[key]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ix, nil
}

// Save persists the index to the local cache.
func (ix *Index) Save() error {
	return cache.Store(CacheKey(ix.Root), ix)
}

// Search returns the top-k chunks most similar to query, best first.
func (ix *Index) Search(query string, k int) []Score {
	q := Embed(query)
	var scores []Score
	for _, d := range ix.Docs {
		scores = append(scores, Score{Doc: d, Sim: Cosine(q, d.Vec)})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].Sim != scores[j].Sim {
			return scores[i].Sim > scores[j].Sim
		}
		return scores[i].Doc.ID < scores[j].Doc.ID
	})
	if k <= 0 || k > len(scores) {
		k = len(scores)
	}
	if len(scores) > k {
		scores = scores[:k]
	}
	return scores
}

// ChunkText splits text into contiguous chunks of at most maxChars, keeping
// paragraphs together where possible. Chunks shorter than 40 chars are
// dropped (they carry no retrieval value). Start is the 1-based line number.
func ChunkText(text string, maxChars int) []Chunk {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	start := 1
	var cur []string
	curLen := 0
	flush := func() {
		if curLen >= 40 {
			chunks = append(chunks, Chunk{Start: start, Text: strings.Join(cur, "\n")})
		}
		cur = nil
		curLen = 0
	}
	for i, ln := range lines {
		l := strings.TrimSpace(ln)
		isBlank := l == ""
		sep := 1
		if isBlank {
			sep = 2
		}
		if curLen > 0 && curLen+len(ln)+sep > maxChars {
			flush()
			start = i + 1
		}
		if curLen == 0 && isBlank {
			start = i + 2
			continue
		}
		if isBlank {
			// blank line ends a paragraph only when it actually separates
			// content; drop consecutive blanks.
			if len(cur) > 0 {
				cur = append(cur, "")
			}
			curLen += 1
			continue
		}
		cur = append(cur, ln)
		curLen += len(ln) + 1
	}
	flush()
	return chunks
}

const vecDim = 4096

// Embed maps text to a fixed-dimension sparse vector using feature-hashed
// word and character n-grams. Deterministic: identical text always yields an
// identical vector, so a locally-embedded query matches locally-embedded
// documents exactly.
func Embed(text string) map[int]float64 {
	vec := make(map[int]float64)
	lower := strings.ToLower(text)
	for _, tok := range tokenizeWords(lower) {
		grams := ngrams(tok)
		for _, g := range grams {
			h := int(fnv32(g) % vecDim)
			if sign(g) {
				vec[h]++
			} else {
				vec[h]--
			}
		}
	}
	l2 := 0.0
	for _, v := range vec {
		l2 += v * v
	}
	if l2 > 0 {
		inv := 1 / math.Sqrt(l2)
		for k, v := range vec {
			vec[k] = v * inv
		}
	}
	return vec
}

// Cosine similarity between two embeddings.
func Cosine(a, b map[int]float64) float64 {
	var dot float64
	if len(a) > len(b) {
		a, b = b, a
	}
	for k, va := range a {
		if vb, ok := b[k]; ok {
			dot += va * vb
		}
	}
	return dot
}

var wordRe = regexp.MustCompile(`[a-z0-9_]+`)

func tokenizeWords(s string) []string {
	return wordRe.FindAllString(s, -1)
}

// ngrams yields the char 3-grams of a word plus the whole word.
func ngrams(word string) []string {
	if len(word) < 3 {
		return []string{word}
	}
	out := make([]string, 0, len(word))
	for i := 0; i+3 <= len(word); i++ {
		out = append(out, word[i:i+3])
	}
	return out
}

func fnv32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// sign returns true for odd top bits, giving each feature a random sign
// (a random-projection flavour of feature hashing).
func sign(s string) bool {
	return fnv32(s)&1 == 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
