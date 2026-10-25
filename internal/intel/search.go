package intel

import (
	"container/list"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
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
	scoredList := make([]*scored, len(pool))
	var wg sync.WaitGroup
	// Embed concurrency is capped at 4 (was 8): the local embedding model is
	// the bottleneck, and extra goroutines only queue requests. Descriptors
	// already cached in this process are scored inline — a cheap map-membership
	// check (cachedEmbedding) skips both the goroutine and the embed cost.
	sem := make(chan struct{}, 4)
	for idx, s := range pool {
		desc := symbolDescriptor(s)
		if v, ok := cachedEmbedding(desc); ok {
			scoredList[idx] = &scored{s: s, cos: denseCosine(qvec, v), rank: idx}
			continue
		}
		wg.Add(1)
		go func(rank int, sym index.Symbol, desc string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			vec, err := embedCached(e, desc)
			if err != nil {
				return
			}
			scoredList[rank] = &scored{s: sym, cos: denseCosine(qvec, vec), rank: rank}
		}(idx, s, desc)
	}
	wg.Wait()

	var list []scored
	for _, sc := range scoredList {
		if sc != nil {
			list = append(list, *sc)
		}
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
	symbolEmbedCache = map[string]*embedCacheEntry{}
	// symbolEmbedOrder is the LRU recency list of cache keys (front = most
	// recently used): on overflow the LEAST-recently-used key is evicted.
	// Every hit refreshes recency (move-to-front), so hot descriptors survive
	// cold scans that would have evicted them under FIFO. Kept in lockstep
	// with symbolEmbedCache.
	symbolEmbedOrder list.List
	symbolEmbedCap   = 1000
)

// embedCacheEntry pairs a cached embedding with its recency-list element so a
// hit can refresh LRU recency in O(1).
type embedCacheEntry struct {
	vec []float32
	el  *list.Element
}

func embedCached(e SymbolEmbedder, desc string) ([]float32, error) {
	symbolEmbedMu.Lock()
	if entry, ok := symbolEmbedCache[desc]; ok {
		// LRU: refresh recency on hit — hot entries move to the front.
		symbolEmbedOrder.MoveToFront(entry.el)
		symbolEmbedMu.Unlock()
		return entry.vec, nil
	}
	symbolEmbedMu.Unlock()
	v, err := e.EmbedText(desc)
	if err != nil {
		return nil, err
	}
	symbolEmbedMu.Lock()
	for len(symbolEmbedCache) >= symbolEmbedCap {
		// LRU eviction: drop the least-recently-used key (the back of the
		// recency list) instead of the oldest-inserted one, so hot
		// descriptors survive cold scans.
		if el := symbolEmbedOrder.Back(); el != nil {
			oldest := el.Value.(string)
			symbolEmbedOrder.Remove(el)
			delete(symbolEmbedCache, oldest)
			continue
		}
		break
	}
	// The map check guards the recency list against duplicate keys when two
	// goroutines embed the same descriptor concurrently (one already inserted
	// it between our miss above and this lock). Keep the existing entry and
	// refresh its recency; the freshly computed vector is discarded.
	if entry, ok := symbolEmbedCache[desc]; ok {
		symbolEmbedOrder.MoveToFront(entry.el)
	} else {
		el := symbolEmbedOrder.PushFront(desc)
		symbolEmbedCache[desc] = &embedCacheEntry{vec: v, el: el}
	}
	symbolEmbedMu.Unlock()
	return v, nil
}

// cachedEmbedding returns a descriptor's cached embedding without triggering
// an embed — the cheap membership test used to skip goroutines (and embed
// cost) for symbols already embedded in this process.
func cachedEmbedding(desc string) ([]float32, bool) {
	symbolEmbedMu.Lock()
	defer symbolEmbedMu.Unlock()
	entry, ok := symbolEmbedCache[desc]
	if !ok {
		return nil, false
	}
	// LRU: a membership probe is an access too — refresh recency.
	symbolEmbedOrder.MoveToFront(entry.el)
	return entry.vec, true
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
	scored := RankedSearchScored(ix, query, limit)
	out := make([]index.Symbol, len(scored))
	for i, h := range scored {
		out[i] = h.Symbol
	}
	return out
}

// RankedSearchScored returns symbols matching a free-text query with their match scores.
// The single-token identifier gate (P0-1) applies: an identifier-shaped query
// that names no real symbol returns zero hits so resolution stays honest.
func RankedSearchScored(ix *index.Index, query string, limit int) []RepoHit {
	return rankedSearchScored(ix, query, limit, true)
}

// CloseCandidates returns the top ranked-search hits for a query WITHOUT the
// single-token identifier gate (P0-1). The gate exists to stop garbage
// identifiers from RESOLVING to unrelated fuzzy hits; a did-you-mean /
// "close candidates" list is not a resolution — it is explicitly labeled, so
// the agent sees what the index contains near the query and can correct the
// follow-up search. Mirrors kern impact's closest-candidates machinery.
func CloseCandidates(ix *index.Index, query string, limit int) []RepoHit {
	return rankedSearchScored(ix, query, limit, false)
}

// lowerMirrorCache memoizes the lowercase name/full-name/file mirrors per
// index instance (cachedLowerMirrors). Published indexes are immutable once
// built — internal/web/web.go relies on this — so the mirrors are computed
// once per index instead of once per search (~60k strings.ToLower +
// FullName() allocations per call at ~20k symbols). sync.Map because the
// search path fans out across goroutines (MCP servers, web handlers).
var lowerMirrorCache sync.Map // *index.Index -> *lowerMirrors

// lowerMirrors holds the per-symbol lowercase mirrors consumed by
// rankedSearchScored: one strings.ToLower per Name, FullName() and File, in
// ix.Symbols order, so the scoring loop never repeats that allocation.
type lowerMirrors struct {
	names []string
	fulls []string
	files []string
}

// cachedLowerMirrors returns ix's lowercase mirrors, building them on first
// use and caching them for the index's lifetime. The len(lm.names) guard
// re-computes defensively if a caller ever mutates an index in place: a
// length change invalidates the cached mirrors, and the stale entry is
// deleted first so the LoadOrStore below cannot hand it back. Content edits
// that keep the length are outside the contract — the index is immutable
// once published.
func cachedLowerMirrors(ix *index.Index) *lowerMirrors {
	if v, ok := lowerMirrorCache.Load(ix); ok {
		lm := v.(*lowerMirrors)
		if len(lm.names) == len(ix.Symbols) {
			return lm
		}
		lowerMirrorCache.Delete(ix)
	}
	lm := &lowerMirrors{
		names: make([]string, len(ix.Symbols)),
		fulls: make([]string, len(ix.Symbols)),
		files: make([]string, len(ix.Symbols)),
	}
	for i, s := range ix.Symbols {
		lm.names[i] = strings.ToLower(s.Name)
		lm.fulls[i] = strings.ToLower(s.FullName())
		lm.files[i] = strings.ToLower(s.File)
	}
	actual, _ := lowerMirrorCache.LoadOrStore(ix, lm)
	return actual.(*lowerMirrors)
}

func rankedSearchScored(ix *index.Index, query string, limit int, gate bool) []RepoHit {
	if limit <= 0 {
		limit = 50
	}
	words := queryWords(query)
	if len(words) == 0 {
		return nil
	}
	// V7b: a query is code-intent when it names code-ish concepts — the
	// deterministic signal that code kinds should be promoted. Doc queries
	// ("what is the architecture", "how does X work") keep the plain
	// ranking.
	codeIntent := isCodeIntentQuery(query)
	// I5 (deep-dive A2, 2026-10-03): markdown headings are prose, not
	// symbols — they are EXCLUDED from ranked search entirely unless the
	// query is doc-shaped ("what is…", "how does…", "explain…"), where a
	// heading hit is genuinely the best answer. This is the structural fix
	// for heading pollution feeding kern_search/kern_retrieve/kern_plan
	// ("Test with curl" as a plan "test" — a doc heading, not code).
	// docIntent wins over codeIntent: doc questions about code ("how does
	// the api server work") still rank code first via the codeIntent
	// demotion below, but headings stay reachable.
	includeHeadings := isDocIntentQuery(query)
	// Lowercase name/full-name/file mirrors are memoized per index instance
	// (cachedLowerMirrors): the index is immutable once published, so the
	// strings.ToLower (and FullName concatenation) work happens once per
	// index, not once per search. Semantics are identical: the same lowered
	// forms feed matchWord, and segCache stays keyed on the original Name
	// (segment splitting is case-preserving upstream).
	lm := cachedLowerMirrors(ix)
	// joined is the query's words concatenated — the identifier form that
	// queryWords' camelCase split started from ("stateMachine" -> joined
	// "statemachine"). Used for exact-name scoring below and by the
	// single-token gate.
	joined := strings.Join(words, "")
	// identifierShaped reports whether the raw query is a single no-space
	// token ("NewServer", "stateMachine", "NoSuchSymbol12345") rather than
	// prose ("load index", "login handler"). Identifier-shaped queries get
	// the strict no-match gate below; prose keeps the forgiving fuzzy path.
	identifierShaped := len(strings.Fields(query)) == 1
	// P1: a path-ish query names a FILE, not an identifier. The strict
	// single-token gate below does not apply, and symbols whose file contains
	// the path fragment get a strong boost so "src/tests" surfaces the
	// symbols under src/tests/ ahead of unrelated name matches.
	pathFrag := pathFragment(query)
	pathQuery := pathFrag != ""
	segCache := map[string][]string{}
	var hits []RepoHit
	for i, s := range ix.Symbols {
		if s.Kind == "heading" && !includeHeadings {
			continue
		}
		name := lm.names[i]
		full := lm.fulls[i]
		file := lm.files[i]
		segs, ok := segCache[s.Name]
		if !ok {
			segs = tokenize.SplitIdentifierSegments(s.Name)
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
		fragMatched := pathFrag != "" && strings.Contains(file, pathFrag)
		if fragMatched {
			// The path fragment IS the whole query: matching it counts as
			// matching every word and outranks any unrelated name match
			// (P1) — "src/tests" must surface the symbols under src/tests/
			// ahead of a symbol merely named "Tests".
			score += 120
			matched = len(words)
		}
		if matched == 0 {
			continue
		}
		if matched == len(words) {
			score += 150 // matches every token: boost strongly
		}
		// F-SE1: an exact-name hit IS the definition — it must outrank any
		// longer symbol that merely contains the query (agents take the top
		// hit, and TestWriteFileAtomic must never shadow WriteFileAtomic).
		// queryWords camelCase-splits, so compare the JOINED query too
		// (joined is hoisted above the loop: identical every iteration).
		if joined != "" {
			if joined == name || joined == full {
				score += 200
			} else if alnumForm(s.Name) == joined || alnumForm(s.FullName()) == joined {
				// queryWords treats "_" as a word separator, so the
				// joined form of "Fn0_5" is "fn05" — it can never equal
				// the real name "fn0_5". Compare separator-stripped
				// forms so the definitional exact-name boost still lands
				// on digit-suffixed identifiers.
				score += 200
			}
		}
		if codeIntent {
			// V7b: for code-oriented queries, code symbols must beat prose
			// headings ("What Is Conduit?" matches query words trivially
			// but a developer looking for symbols wants the type/func).
			// (Headings only reach here on doc-intent queries — see
			// includeHeadings above.)
			if s.Kind == "heading" {
				score -= 100
			} else if isCodeKind(s.Kind) {
				score += 30
			}
		}
		if ix.IsGenerated(s.File) {
			score -= 60
		}
		// F6 (2026-10-07): symbols defined in test files or testdata fixtures
		// are demoted by a fixed 100-point penalty so production symbols rank
		// above them by default. Demotion is NOT exclusion: a test/fixture
		// symbol that is the only or best match still surfaces. The penalty
		// keeps the resolveSymbol/ResolveFuzzy fallback contract intact: a
		// matched-all test hit carries at least ~240 raw (90 segment match +
		// 150 all-match boost) before this penalty, staying above the 150
		// high-confidence gate those resolvers require.
		// P1: an EXPLICIT path query that matched the file waives the penalty
		// — the user pointed at that exact path ("src/tests"), so the
		// production-first ordering intent does not apply to it.
		if (IsTestFile(s.File) || IsFixtureFile(s.File)) && !fragMatched {
			score -= 100
		}
		hits = append(hits, RepoHit{Symbol: s, Score: score, MatchedAll: matched == len(words)})
	}
	// Single-token identifier gate (P0-1): an identifier-shaped query names
	// one specific symbol, so it must not resolve to coincidental partial
	// matches. "NoSuchSymbol12345" camelCase-splits to no/such/symbol12345
	// and the 2-char "no" segment used to match ErrNoProvider — garbage
	// identifiers always returned unrelated fuzzy hits with exit 0, and an
	// agent trusted the top hit as the answer. When NO hit matched every
	// query word (MatchedAll) and no hit's name exactly equals or starts
	// with the joined query, the identifier does not exist in this index:
	// return zero hits so the caller's no-match path (exit 1 + did-you-mean)
	// fires. Prose queries (spaces) and single-word queries (any hit is
	// MatchedAll) are untouched. With gate=false (CloseCandidates) the gate is
	// skipped so a did-you-mean list can still show partial fuzzy hits.
	if gate && identifierShaped && !pathQuery && !hasStrongIdentifierMatch(hits, joined) {
		return nil
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Symbol.FullName() != hits[j].Symbol.FullName() {
			return hits[i].Symbol.FullName() < hits[j].Symbol.FullName()
		}
		return hits[i].Symbol.File < hits[j].Symbol.File
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// hasStrongIdentifierMatch reports whether any hit is strong evidence that an
// identifier-shaped query (a single no-space token) names a real symbol in
// this index. Strong means the hit matched EVERY query word (MatchedAll — e.g.
// "NewServer" splitting to new+server) or its name exactly equals / starts
// with the joined camelCase query ("NewServ" still prefix-matches
// NewServer). Coincidental segment or substring hits ("no" -> ErrNoProvider)
// are NOT strong: they are why garbage identifiers like "NoSuchSymbol12345"
// used to return unrelated fuzzy results with exit 0.
func hasStrongIdentifierMatch(hits []RepoHit, joined string) bool {
	if joined == "" {
		return false
	}
	for _, h := range hits {
		if h.MatchedAll {
			return true
		}
		name := strings.ToLower(h.Symbol.Name)
		if name == joined || strings.HasPrefix(name, joined) {
			return true
		}
		// The joined query drops separators ("Fn0_5" -> "fn05"), so it
		// never equals the raw name. The separator-stripped name must
		// count as naming the identifier.
		if an := alnumForm(h.Symbol.Name); an == joined || strings.HasPrefix(an, joined) {
			return true
		}
	}
	return false
}

// queryWords splits and normalizes a free-text query into matchable words:
// separated on any non-letter/digit run ("state-machine", "load index") AND on
// camelCase humps ("stateMachine" -> state/machine), then lowercased with
// Latin diacritics stripped. Splitting before lowercasing preserves the hump
// signal so a camelCase query names its concept in whole segments.
func queryWords(query string) []string {
	seen := map[string]bool{}
	var words []string
	for _, run := range tokenize.WordRuns(query) {
		for _, part := range tokenize.SplitCamel(run) {
			w := tokenize.NormalizeProseWord(part)
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

// isPathQuery reports whether a query names a file-path fragment rather than
// a symbol: it contains a path separator ("src/tests", "internal\util") or
// uses the explicit file: prefix ("file:cmd/kern/cmd_optimize.go"). Path
// queries get the file-match boost in rankedSearchScored so symbols under the
// named path outrank unrelated name matches (P1), and they skip the strict
// single-token identifier gate — a path fragment is never a "garbage
// identifier".
func isPathQuery(q string) bool {
	lq := strings.ToLower(strings.TrimSpace(q))
	if strings.HasPrefix(lq, "file:") {
		return true
	}
	return strings.Contains(lq, "/") || strings.Contains(lq, `\`)
}

// pathFragment returns the normalized path fragment of a path query: the
// file: prefix stripped, separators folded to "/", lowercased and trimmed of
// surrounding slashes. It is "" for non-path queries. The fragment is matched
// as a SUBSTRING of a symbol's file path, so "src/tests" matches
// "src/tests/helper.go" and "file:cmd/kern/cmd_optimize.go" matches the
// symbols defined in exactly that file.
func pathFragment(q string) string {
	if !isPathQuery(q) {
		return ""
	}
	frag := strings.TrimSpace(q)
	if i := strings.Index(frag, ":"); i >= 0 && strings.HasPrefix(strings.ToLower(frag), "file:") {
		frag = frag[i+1:]
	}
	frag = strings.ReplaceAll(frag, `\`, "/")
	return strings.ToLower(strings.Trim(frag, "/"))
}

// matchWord scores how strongly one normalized query word matches a symbol.
// Only the strongest tier for the word counts. Segment tiers are whole-word
// evidence: an exact segment match outranks prefix/substring hits, and a
// plural-folded segment ("services" -> service) still hits its symbol.
// The substring tiers require a minimum query-word length of 3: a 2-char
// fragment from a camelCase split (e.g. "ZzZzNopeNope" -> "zz") would
// otherwise substring-match every symbol containing a coincidental 2-char
// run. 2-char searches like "io" or "db" still work through the exact/
// segment/prefix tiers above. The file tier (P1) sits between the full-name
// and name substring tiers: a word present in a symbol's FILE is weaker than
// the same word in its name, but far from useless — a path-ish query whose
// word only appears in file paths still ranks those files' symbols above
// unrelated name-substring noise.
func matchWord(w, name, full, file string, segs []string) int {
	switch {
	case name == w:
		return 100
	case full == w:
		return 95
	case segmentExact(w, segs):
		return 90
	case tokenize.IsAllDigits(w) && containsDigitRun(name, w):
		return 85
	case strings.HasPrefix(full, w):
		return 80
	case strings.HasPrefix(name, w):
		return 70
	case segmentFolded(w, segs):
		return 66
	case len(w) >= 3 && strings.Contains(name, w):
		return 60
	case len(w) >= 3 && strings.Contains(full, w):
		return 45
	case len(w) >= 3 && strings.Contains(file, w):
		return 50
	}
	return 0
}

// containsDigitRun reports whether any maximal digit run in name equals the
// all-digit query word w ("79" hits "Fn59_79"; "9" does not).
// splitIdentifierSegments drops all-digit segments, so a 1–2 digit word after
// a separator could never match — matchWord returned 0, the query never
// reached MatchedAll, and the P0-1 gate zeroed the whole result even though
// the identifier named a real symbol. This tier restores it without touching
// ranking semantics for non-digit words. Identifiers use ASCII digits, so a
// byte scan is safe: multi-byte runes never pass IsDigit.
func containsDigitRun(name, w string) bool {
	i := 0
	for i < len(name) {
		if !unicode.IsDigit(rune(name[i])) {
			i++
			continue
		}
		j := i
		for j < len(name) && unicode.IsDigit(rune(name[j])) {
			j++
		}
		if name[i:j] == w {
			return true
		}
		i = j
	}
	return false
}

// alnumForm lowercases s and strips every non-alphanumeric rune: the
// separator-insensitive identifier form ("Fn0_5" and "fn05" compare equal).
// Used by the exact-name boost and the P0-1 gate so a separator dropped by
// queryWords' word-splitting cannot make a real identifier read as garbage.
func alnumForm(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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
	for _, v := range tokenize.SegmentLookupVariants(w) {
		if v == w {
			continue
		}
		if segmentExact(v, segs) {
			return true
		}
	}
	return false
}

// isCodeKind reports whether a symbol kind is executable/type code rather
// than prose content (headings, entries, properties).
func isCodeKind(kind string) bool {
	switch kind {
	case "func", "method", "class", "struct", "interface", "type", "enum",
		"trait", "union", "const", "var", "module", "impl":
		return true
	}
	return false
}

// isCodeIntentQuery heuristically detects code-oriented search queries
// ("how is the api server wired", "find the login handler") vs doc queries
// ("what is the architecture"). Deterministic keyword matching — no LLM.
func isCodeIntentQuery(q string) bool {
	lq := strings.ToLower(q)
	for _, kw := range []string{
		"api", "function", "method", "handler", "server", "symbol", "class",
		"endpoint", "wired", "service", "code", "interface", "struct", "bug",
		"error", "crash", "call", "caller", "implement", "route", "config",
	} {
		if strings.Contains(lq, kw) {
			return true
		}
	}
	return false
}

// isDocIntentQuery heuristically detects documentation-shaped queries where
// a markdown heading hit is genuinely the best answer ("what is the retry
// policy", "how does indexing work", "explain the budget model", "where are
// the docs"). Deterministic phrase matching — no LLM. Doc intent wins over
// code intent for heading inclusion (I5/A2): a doc question about code
// keeps headings reachable while code symbols still rank first.
func isDocIntentQuery(q string) bool {
	lq := " " + strings.ToLower(strings.TrimSpace(q)) + " "
	for _, phrase := range []string{
		"what is", "what are", "what does", "what's",
		"how does", "how do", "how is", "how are", "how to",
		"explain", "describe", "overview", "summary of", "documentation",
		"docs", "doc ", "guide", "tutorial", "readme", "changelog",
		"where is the doc", "introduction",
	} {
		if strings.Contains(lq, phrase) {
			return true
		}
	}
	return false
}

// CollapseModuleDuplicates de-duplicates search results whose full name and
// kind are identical but which live in DIFFERENT top-level module
// directories (V7d: multi-module repos like Maven parents surface the same
// class once per module — correct but noisy). The first occurrence per
// module is kept; the second return value counts collapsed entries so
// renderers can annotate the total.
func CollapseModuleDuplicates(syms []index.Symbol) ([]index.Symbol, int) {
	seen := map[string]string{} // fullname+kind -> module root
	kept := make([]index.Symbol, 0, len(syms))
	collapsed := 0
	for _, s := range syms {
		module := moduleRoot(s.File)
		key := s.FullName() + "|" + s.Kind
		if prev, dup := seen[key]; dup && prev != module {
			collapsed++
			continue
		}
		if _, dup := seen[key]; !dup {
			seen[key] = module
		}
		kept = append(kept, s)
	}
	return kept, collapsed
}

// moduleRoot returns the top-level directory a file lives in, or the whole
// path when there is none (e.g. "main.go").
func moduleRoot(file string) string {
	i := strings.IndexByte(file, '/')
	if i < 0 {
		return file
	}
	return file[:i]
}
