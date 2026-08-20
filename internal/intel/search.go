package intel

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// SymbolEmbedder produces dense embeddings for code symbol descriptors.
// internal/llm.Client implements it against a local Ollama server. It is
// optional: without it SemanticSearch degrades to RankedSearch.
type SymbolEmbedder interface {
	EmbedText(text string) ([]float32, error)
}

// SemanticSearch ranks symbols for query using the deterministic n-gram
// matcher and, when an embedder is available, dense embeddings fused by
// reciprocal rank. The deterministic pass widens the candidate pool (bounded
// embedding cost); the dense signal re-orders it by meaning. Without an
// embedder it degrades to RankedSearch.
func SemanticSearch(ix *index.Index, query string, limit int, e SymbolEmbedder) []index.Symbol {
	if limit <= 0 {
		limit = 20
	}
	poolLimit := limit * 4
	if poolLimit < 40 {
		poolLimit = 40
	}
	pool := RankedSearch(ix, query, poolLimit)
	if len(pool) == 0 {
		return nil
	}
	if e == nil {
		return truncate(pool, limit)
	}
	qvec, err := e.EmbedText(query)
	if err != nil {
		return truncate(pool, limit)
	}
	type scored struct {
		s    index.Symbol
		cos  float64 // dense cosine to query
		rank int     // deterministic rank (0-based)
	}
	var list []scored
	for rank, s := range pool {
		vec, err := embedCached(e, symbolDescriptor(s))
		if err != nil {
			continue
		}
		list = append(list, scored{s: s, cos: denseCosine(qvec, vec), rank: rank})
	}
	if len(list) == 0 {
		return truncate(pool, limit)
	}
	const rrfK = 60
	type acc struct {
		s    index.Symbol
		scor float64
	}
	byKey := map[string]acc{}
	for i, sc := range list {
		key := symbolKey(sc.s)
		a := byKey[key]
		a.s = sc.s
		a.scor += 1 / (rrfK + float64(i) + 1) // dense rank
		a.scor += 1 / (rrfK + float64(sc.rank) + 1)
		byKey[key] = a
	}
	out := make([]index.Symbol, 0, len(byKey))
	for _, a := range byKey {
		out = append(out, a.s)
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := symbolKey(out[i]), symbolKey(out[j])
		if byKey[ki].scor != byKey[kj].scor {
			return byKey[ki].scor > byKey[kj].scor
		}
		if out[i].FullName() != out[j].FullName() {
			return out[i].FullName() < out[j].FullName()
		}
		return out[i].File < out[j].File
	})
	return truncate(out, limit)
}

// symbolEmbedCache caches symbol-descriptor embeddings within a process so a
// long-lived MCP session re-ranks without re-embedding the same symbols.
var (
	symbolEmbedMu    sync.Mutex
	symbolEmbedCache = map[string][]float32{}
	symbolEmbedCap   = 1000
)

func embedCached(e SymbolEmbedder, desc string) ([]float32, error) {
	symbolEmbedMu.Lock()
	if v, ok := symbolEmbedCache[desc]; ok {
		symbolEmbedMu.Unlock()
		return v, nil
	}
	symbolEmbedMu.Unlock()
	v, err := e.EmbedText(desc)
	if err != nil {
		return nil, err
	}
	symbolEmbedMu.Lock()
	if len(symbolEmbedCache) >= symbolEmbedCap {
		for k := range symbolEmbedCache {
			delete(symbolEmbedCache, k)
			break
		}
	}
	symbolEmbedCache[desc] = v
	symbolEmbedMu.Unlock()
	return v, nil
}

// symbolDescriptor is the compact text that represents a symbol to an
// embedding model: kind, qualified name, params, file, and route when the
// symbol is a framework entry point.
func symbolDescriptor(s index.Symbol) string {
	var b strings.Builder
	if s.Kind != "" {
		b.WriteString(s.Kind)
		b.WriteString(" ")
	}
	b.WriteString(s.FullName())
	if len(s.Params) > 0 {
		b.WriteString("(")
		b.WriteString(strings.Join(s.Params, ", "))
		b.WriteString(")")
	}
	b.WriteString(" ")
	b.WriteString(s.File)
	if s.Entry && s.Route != "" {
		b.WriteString(" route ")
		b.WriteString(s.Route)
	}
	return b.String()
}

func symbolKey(s index.Symbol) string {
	return s.File + ":" + strconv.Itoa(s.Line) + ":" + s.FullName()
}

func truncate(in []index.Symbol, limit int) []index.Symbol {
	if limit >= len(in) {
		return in
	}
	return in[:limit]
}

// denseCosine is the cosine similarity between two dense vectors (unnormalized
// dot product — Ollama embeddings are unit vectors).
func denseCosine(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot float64
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

// RankedSearch returns symbols matching a free-text query, ranked by match
// strength: exact name > exact full name > whole-segment match > prefix >
// plural/diacritic-folded segment > substring > file-name hit. Multi-word
// queries are split and symbols matching more words rank higher. Query words
// are normalized (lowercased, diacritics stripped, camelCase split into
// segments) so prose like "state machine" or "résolution" hits
// OrderStateMachine / resolveUnresolved. Generated files are penalized so
// hand-written implementations of the same name rank first. Unlike
// Index.Search (AST wildcard patterns) this is forgiving free-text lookup.
func RankedSearch(ix *index.Index, query string, limit int) []index.Symbol {
	if limit <= 0 {
		limit = 50
	}
	words := queryWords(query)
	if len(words) == 0 {
		return nil
	}
	segCache := map[string][]string{}
	type hit struct {
		s     index.Symbol
		score int
	}
	var hits []hit
	for _, s := range ix.Symbols {
		name := strings.ToLower(s.Name)
		full := strings.ToLower(s.FullName())
		file := strings.ToLower(s.File)
		segs, ok := segCache[s.Name]
		if !ok {
			segs = splitIdentifierSegments(s.Name)
			segCache[s.Name] = segs
		}
		var score, matched int
		for _, w := range words {
			sc := matchWord(w, name, full, file, segs)
			if sc > 0 {
				score += sc
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		if matched == len(words) {
			score += 150 // matches every token: boost strongly
		}
		if ix.IsGenerated(s.File) {
			score -= 60 // demote generated stubs below real implementations
		}
		hits = append(hits, hit{s: s, score: score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].s.FullName() != hits[j].s.FullName() {
			return hits[i].s.FullName() < hits[j].s.FullName()
		}
		return hits[i].s.File < hits[j].s.File
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]index.Symbol, len(hits))
	for i, h := range hits {
		out[i] = h.s
	}
	return out
}

// queryWords splits and normalizes a free-text query into matchable words:
// separated on any non-letter/digit run ("state-machine", "load index") AND on
// camelCase humps ("stateMachine" -> state/machine), then lowercased with
// Latin diacritics stripped. Splitting before lowercasing preserves the hump
// signal so a camelCase query names its concept in whole segments.
func queryWords(query string) []string {
	seen := map[string]bool{}
	var words []string
	for _, run := range wordRuns(query) {
		for _, part := range splitCamel(run) {
			w := normalizeProseWord(part)
			if w == "" {
				continue
			}
			if !seen[w] {
				seen[w] = true
				words = append(words, w)
			}
		}
	}
	return words
}

// matchWord scores how strongly one normalized query word matches a symbol.
// Only the strongest tier for the word counts. Segment tiers are whole-word
// evidence: an exact segment match outranks prefix/substring hits, and a
// plural-folded segment ("services" -> service) still hits its symbol.
func matchWord(w, name, full, file string, segs []string) int {
	switch {
	case name == w:
		return 100
	case full == w:
		return 95
	case segmentExact(w, segs):
		return 90
	case strings.HasPrefix(full, w):
		return 80
	case strings.HasPrefix(name, w):
		return 70
	case segmentFolded(w, segs):
		return 66
	case strings.Contains(name, w):
		return 60
	case strings.Contains(full, w):
		return 45
	case strings.Contains(file, w):
		return 20
	}
	return 0
}

func segmentExact(w string, segs []string) bool {
	for _, seg := range segs {
		if seg == w {
			return true
		}
	}
	return false
}

// segmentFolded matches when a plural variant of the query word equals a
// segment ("services" hitting the segment "service"). The query word itself is
// already diacritic-normalized upstream.
func segmentFolded(w string, segs []string) bool {
	for _, v := range segmentLookupVariants(w) {
		if v == w {
			continue
		}
		if segmentExact(v, segs) {
			return true
		}
	}
	return false
}
