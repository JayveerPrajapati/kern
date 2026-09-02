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
	"strconv"
	"strings"
	"sync"

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
	ID    string          `json:"id"`
	Chunk Chunk           `json:"chunk"`
	Vec   map[int]float64 `json:"vec"`
	// Semantic is an optional dense embedding (e.g. from a local Ollama
	// model) added by IndexDirSemantic. When present it is fused into Search
	// alongside the deterministic hashed Vec and BM25.
	Semantic []float32 `json:"semantic,omitempty"`
}

// Embedder produces dense semantic embeddings for text. internal/llm.Client
// implements it against a local Ollama server. It is optional: docsearch's
// deterministic n-gram embedding remains the always-available fallback.
type Embedder interface {
	EmbedText(text string) ([]float32, error)
}

// Index is a set of embedded chunks for one root.
type Index struct {
	Root string `json:"root"`
	Docs []Doc  `json:"docs"`

	// mu serializes concurrent reads (Search) against in-place writes
	// (MergeFetched, Save). Docs is mutated by reference elsewhere, so all
	// access to ix.Docs must go through this mutex.
	mu sync.RWMutex
}

// Score ranks a chunk against a query.
type Score struct {
	Doc Doc
	Sim float64
}

var extRe = regexp.MustCompile(`\.(md|markdown|txt|rst|adoc|asciidoc|org)$`)

// Resource bounds mirror internal/fw/detect.go: cap per-file size and the
// total number of files indexed so a large corpus can't exhaust heap, and cap
// the size of externally fetched pages before chunking.
const (
	maxFileSize    = 2 << 20 // 2 MiB: skip files larger than this
	maxFileCount   = 5000    // cap total files indexed per root
	maxFetchedSize = 4 << 20 // 4 MiB: skip fetched pages larger than this
)

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
	fileCount := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Never skip the root itself: "." would match the hidden-dir
			// rule and silently skip the whole tree.
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "vendor" || d.Name() == "target" || d.Name() == "build" || d.Name() == "dist" || d.Name() == "out") {
				return filepath.SkipDir
			}
			return nil
		}
		if !extRe.MatchString(strings.ToLower(d.Name())) {
			return nil
		}
		// Cap the total number of files indexed so a huge corpus can't
		// exhaust memory.
		if fileCount >= maxFileCount {
			return nil
		}
		// Stat before reading so oversized files are skipped without ever
		// being slurped into heap.
		info, ierr := d.Info()
		if ierr != nil || info.Size() > maxFileSize {
			return nil
		}
		fileCount++
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		for _, c := range ChunkText(string(b), 2000) {
			key := rel + "#" + strconv.Itoa(c.Start)
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
	// Re-merge documents previously fetched for this project (kern doc fetch /
	// kern_doc_fetch). Fetched pages live in the global docs-fetch cache and
	// only reach this project's persisted index via MergeFetched, so a full
	// re-index would silently drop them without this step.
	for _, name := range fetchNames(root) {
		text, err := os.ReadFile(cache.Path("data", "docs-fetch", name+".md"))
		if err != nil {
			continue // cache entry evicted or missing; nothing to re-merge
		}
		ix.mu.Lock()
		ix.mergeFetchedLocked(name, string(text))
		ix.mu.Unlock()
	}
	return ix, nil
}

// IndexDirSemantic is IndexDir plus an optional dense embedding pass: every
// chunk is also embedded through e and stored in Doc.Semantic. Embedding
// failures for individual chunks are skipped (the deterministic Vec is always
// present), so a partially-available model still yields a usable index.
func IndexDirSemantic(root string, e Embedder) (*Index, error) {
	ix, err := IndexDir(root)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return ix, nil
	}
	for i := range ix.Docs {
		vec, err := e.EmbedText(ix.Docs[i].Chunk.Text)
		if err != nil {
			continue
		}
		ix.Docs[i].Semantic = vec
	}
	return ix, nil
}

// Save persists the index to the local cache.
func (ix *Index) Save() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return cache.Store(CacheKey(ix.Root), ix)
}

// fetchPrefix marks documents merged from external web pages so they stay
// distinguishable from on-disk project documents.
const fetchPrefix = "fetch/"

// fetchMapKey persists, per absolute project root, the names of externally
// fetched documents merged into that root's doc index, so a later full
// re-index (IndexDir+Save) can re-merge them instead of silently dropping
// them. Stored in the global cache (not the project) to keep the project tree
// clean and to preserve per-project isolation: a project only ever sees the
// pages fetched for itself.
const fetchMapKey = "docs-fetch-map"

func fetchNames(root string) []string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	m := map[string][]string{}
	if err := cache.Load(fetchMapKey, &m); err != nil {
		return nil
	}
	return m[abs]
}

func rememberFetch(root, name string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return
	}
	m := map[string][]string{}
	_ = cache.Load(fetchMapKey, &m)
	for _, n := range m[abs] {
		if n == name {
			return
		}
	}
	m[abs] = append(m[abs], name)
	_ = cache.Store(fetchMapKey, m)
}

// MergeFetched merges an externally fetched document into root's persisted
// index under "fetch/<name>.md", replacing any prior version of that document
// and persisting the index. The text is chunked and embedded with the same
// deterministic n-gram vectors as on-disk docs, so it is searchable with
// Search. Returns the number of chunks added.
func MergeFetched(root, name, text string) (int, error) {
	// Cap the fetched page size before chunking so a huge page can't exhaust
	// heap.
	if len(text) > maxFetchedSize {
		return 0, nil
	}
	// Persist the raw page in the global docs-fetch cache so a later full
	// re-index (IndexDir) can re-merge it. Callers that already wrote the
	// file (the MCP/CLI fetch handlers) simply overwrite it identically.
	if err := os.MkdirAll(cache.Path("data", "docs-fetch"), 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(cache.Path("data", "docs-fetch", name+".md"), []byte(text), 0o600); err != nil {
		return 0, err
	}
	ix := Load(root)
	if ix == nil {
		var err error
		ix, err = IndexDir(root)
		if err != nil {
			return 0, err
		}
	}
	// Serialize the in-place Doc mutation against concurrent Search reads.
	ix.mu.Lock()
	added := ix.mergeFetchedLocked(name, text)
	ix.mu.Unlock()
	if err := ix.Save(); err != nil {
		return added, err
	}
	rememberFetch(root, name)
	return added, nil
}

// mergeFetchedLocked replaces any prior chunks for fetched document name with
// fresh chunks of text. Callers must hold ix.mu.
func (ix *Index) mergeFetchedLocked(name, text string) int {
	file := fetchPrefix + name + ".md"
	kept := ix.Docs[:0]
	for _, d := range ix.Docs {
		if d.Chunk.File == file {
			continue
		}
		kept = append(kept, d)
	}
	ix.Docs = kept
	added := 0
	for _, c := range ChunkText(text, 2000) {
		ix.Docs = append(ix.Docs, Doc{
			ID:    file + "#" + strconv.Itoa(c.Start),
			Chunk: Chunk{File: file, Start: c.Start, Text: c.Text},
			Vec:   Embed(c.Text),
		})
		added++
	}
	return added
}

// ReembedFetch attaches dense embeddings (via a local Ollama embedder) to the
// fetched document "fetch/<name>.md" already in root's persisted index, then
// saves. Embedding failures for individual chunks are skipped, matching
// IndexDirSemantic. Returns the number of chunks embedded.
func ReembedFetch(root, name string, e Embedder) (int, error) {
	ix := Load(root)
	if ix == nil || e == nil {
		return 0, nil
	}
	file := fetchPrefix + name + ".md"
	n := 0
	for i := range ix.Docs {
		if ix.Docs[i].Chunk.File != file {
			continue
		}
		vec, err := e.EmbedText(ix.Docs[i].Chunk.Text)
		if err != nil {
			continue
		}
		ix.Docs[i].Semantic = vec
		n++
	}
	if err := ix.Save(); err != nil {
		return n, err
	}
	return n, nil
}

// Search returns the top-k chunks most relevant to query, best first. Three
// signals are fused by reciprocal rank: the cosine similarity of the
// feature-hashed n-gram embeddings (fuzzy, morphological), the cosine
// similarity of the optional dense semantic embeddings (real meaning, when the
// index was built with IndexDirSemantic), and BM25 over the chunk's words
// (exact keyword matching). A chunk matching no signal is omitted. Sim holds
// the fused RRF score.
func (ix *Index) Search(query string, k int) []Score {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	q := Embed(query)
	st := buildCorpusStats(ix.Docs)
	queryTerms := tokenizeWords(strings.ToLower(query))

	hasSemantic := false
	for _, d := range ix.Docs {
		if len(d.Semantic) > 0 {
			hasSemantic = true
			break
		}
	}
	var dense []Score
	if emb := GetSemanticEmbedder(); hasSemantic && emb != nil {
		if sq, err := emb.EmbedText(query); err == nil {
			for _, d := range ix.Docs {
				if len(d.Semantic) > 0 {
					dense = append(dense, Score{Doc: d, Sim: denseCosine(sq, d.Semantic)})
				}
			}
		}
	}

	var cosine []Score
	for _, d := range ix.Docs {
		cosine = append(cosine, Score{Doc: d, Sim: Cosine(q, d.Vec)})
	}
	sort.Slice(cosine, func(i, j int) bool {
		if cosine[i].Sim != cosine[j].Sim {
			return cosine[i].Sim > cosine[j].Sim
		}
		return cosine[i].Doc.ID < cosine[j].Doc.ID
	})
	sort.Slice(dense, func(i, j int) bool {
		if dense[i].Sim != dense[j].Sim {
			return dense[i].Sim > dense[j].Sim
		}
		return dense[i].Doc.ID < dense[j].Doc.ID
	})

	var lists [][]Score
	if len(queryTerms) > 0 {
		lists = append(lists, st.rankBM25(queryTerms, ix.Docs))
	}
	if len(dense) > 0 {
		lists = append(lists, dense)
	}
	scores := fuseRRF(append(lists, cosine)...)
	if k <= 0 || k > len(scores) {
		k = len(scores)
	}
	if len(scores) > k {
		scores = scores[:k]
	}
	return scores
}

// SemanticEmbedder, when non-nil, embeds search queries for indexes that carry
// dense Doc.Semantic vectors. It must be the same embedder used to build the
// index (the CLI/MCP layer sets it when it indexed the docs).
// It is written and read from concurrent handler goroutines, so all access is
// serialized through semanticEmbedderMu and the Get/Set accessors below. Do not
// read or write the field directly.
var (
	semanticEmbedderMu sync.Mutex
	SemanticEmbedder   Embedder
)

// GetSemanticEmbedder returns the current SemanticEmbedder (nil when unset),
// guarding the read against concurrent writes.
func GetSemanticEmbedder() Embedder {
	semanticEmbedderMu.Lock()
	defer semanticEmbedderMu.Unlock()
	return SemanticEmbedder
}

// SetSemanticEmbedder atomically replaces the package-level SemanticEmbedder.
func SetSemanticEmbedder(e Embedder) {
	semanticEmbedderMu.Lock()
	defer semanticEmbedderMu.Unlock()
	SemanticEmbedder = e
}

// denseCosine is the cosine similarity (dot / (|a|*|b|)) between two dense
// vectors. True cosine is used because Ollama embeddings are not guaranteed
// to be length-normalized.
func denseCosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// ChunkText splits text into contiguous chunks of at most maxChars, keeping
// paragraphs together where possible. Chunks shorter than 40 chars are
// dropped (they carry no retrieval value). Start is the 1-based line number.
func ChunkText(text string, maxChars int) []Chunk {
	if maxChars <= 0 {
		return nil
	}
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
		// A single line longer than maxChars must never be appended whole,
		// or a chunk could far exceed maxChars. Hard-split it at maxChars
		// boundaries, emitting each piece as its own chunk.
		if len(ln) >= maxChars {
			if curLen > 0 {
				flush()
			}
			for off := 0; off < len(ln); off += maxChars {
				end := off + maxChars
				if end > len(ln) {
					end = len(ln)
				}
				piece := ln[off:end]
				if len(piece) >= 40 {
					chunks = append(chunks, Chunk{Start: i + 1, Text: piece})
				}
			}
			start = i + 1
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
// Three feature types are hashed into the same vector space:
// - Whole words (exact keyword match boost)
// - Word bigrams (phrase matching)
// - Character 3-grams (fuzzy/morphological matching)
// Without whole-word features, the cosine similarity between a query and a
// chunk that shares all the same words can be as low as 0.03 (the char 3-grams
// rarely overlap across different words). Whole-word features ensure that
// exact keyword matches produce a strong signal.
func Embed(text string) map[int]float64 {
	vec := make(map[int]float64)
	lower := strings.ToLower(text)
	words := tokenizeWords(lower)

	// Whole-word features: each word contributes a +1 to its hash slot.
	// This ensures that two texts sharing the same words get a strong
	// cosine boost even if their char n-grams don't overlap.
	for _, tok := range words {
		h := int(fnv32("w:"+tok) % vecDim)
		if sign("w:" + tok) {
			vec[h]++
		} else {
			vec[h]--
		}
	}

	// Word bigram features: consecutive word pairs capture phrase structure
	// ("kafka consumer", "consumer configuration") that single words miss.
	for i := 0; i+1 < len(words); i++ {
		bg := "b:" + words[i] + " " + words[i+1]
		h := int(fnv32(bg) % vecDim)
		if sign(bg) {
			vec[h]++
		} else {
			vec[h]--
		}
	}

	// Character 3-gram features: fuzzy matching for typos, morphological
	// variants and partial word overlap.
	for _, tok := range words {
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
