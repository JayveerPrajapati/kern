package index

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/langparse"
)

// reindexByFile rebuilds the exported SymbolsByFile map and the kindIdx
// lookup buckets from the final symbol table. The build/update sequences
// call this after resolveEntries (which flips Entry on func/method
// symbols), and the load paths call only buildSymbolsByFile, deferring the
// kindIdx buckets to the first kind-filtered Search.
func (ix *Index) reindexByFile() {
	ix.buildSymbolsByFile()
	ix.buildKindIndex()
}

// buildSymbolsByFile rebuilds SymbolsByFile from the final symbol table.
// Unlike the unexported lookup caches (symbolIdx, kindIdx) it cannot be
// deferred on the load paths: SymbolsByFile is an exported field read
// directly by consumers outside this package (intel/guard, intel/changes,
// web, fragility, architecture), so every load must present it. It is the
// cheapest of the three passes — a single O(symbols) append — so keeping
// it eager costs little.
func (ix *Index) buildSymbolsByFile() {
	ix.SymbolsByFile = map[string][]Symbol{}
	for _, s := range ix.Symbols {
		ix.SymbolsByFile[s.File] = append(ix.SymbolsByFile[s.File], s)
	}
}

// buildKindTable computes the kind -> symbols buckets in Symbols order.
// Bucket membership mirrors symbolMatches exactly:
//   - every non-empty Kind gets an exact bucket ("func", "method", ...),
//     except "entry" (a flag, not a Kind) and the searchTypeKinds members,
//     which land in the "type" super-category bucket instead;
//   - every symbol with Entry set lands in the "entry" bucket;
//   - "type" holds every symbol whose Kind is in searchTypeKinds.
//
// Search re-applies symbolMatches on top of the bucket, so a bucket is only
// ever a candidate superset — results and limit behavior are identical to
// the pre-index linear scan.
func buildKindTable(symbols []Symbol) map[string][]Symbol {
	m := make(map[string][]Symbol)
	for _, s := range symbols {
		if s.Kind != "" && s.Kind != "entry" && !searchTypeKinds[s.Kind] {
			m[s.Kind] = append(m[s.Kind], s)
		}
		if searchTypeKinds[s.Kind] {
			m["type"] = append(m["type"], s)
		}
		if s.Entry {
			m["entry"] = append(m["entry"], s)
		}
	}
	return m
}

// buildKindIndex precomputes the kind -> symbols buckets that let
// kind-filtered Search queries iterate only matching-kind symbols instead of
// scanning every one. Called eagerly by the build/update finalize paths
// (via reindexByFile, after resolveEntries) and lazily by the first
// kind-filtered Search on an index that loaded without the buckets. The
// sync.Once makes it idempotent (a second call is a no-op — the table is
// derived from an immutable symbol table) and safe when the deferred build
// races with concurrent readers.
func (ix *Index) buildKindIndex() {
	c := ix.getCache()
	c.kindOnce.Do(func() {
		c.kindIdx = buildKindTable(ix.Symbols)
	})
}

// Languages returns the distinct languages present in the index, sorted.
func (ix *Index) Languages() []string {
	set := map[string]bool{}
	for _, s := range ix.Symbols {
		if s.Lang != "" {
			set[s.Lang] = true
		}
	}
	var out []string
	for l := range set {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

// symbolConflictKinds are kinds treated as declared entities; "entry" and
// "heading" markers legitimately repeat and are exempt.
var symbolConflictKinds = map[string]bool{
	"func": true, "method": true, "type": true, "struct": true,
	"interface": true, "class": true, "enum": true, "trait": true,
	"module": true, "union": true, "impl": true, // "prop" intentionally absent: JSON/YAML data-file keys legitimately repeat on different lines
	"const": true, "var": true, "doc": true,
}

// dropDuplicateIdentities removes later same-identity duplicates from syms,
// mirroring checkFileSymbolConflicts' equality contract exactly: same Name,
// Kind, Receiver, Outer and Params, on a different line/span, among the
// conflict-checked kinds (receiver-bearing or type-kind declarations). The
// first occurrence in slice (document) order wins. Returns the kept slice
// and the dropped symbols. It backs the quarantine policy in
// applyFileResult: one file's extractor anomaly must never fail the whole
// repo's index.
func dropDuplicateIdentities(syms []Symbol) ([]Symbol, []Symbol) {
	type idKey struct {
		name, kind, receiver, outer, params string
	}
	seen := map[idKey]bool{}
	var kept []Symbol
	var dropped []Symbol
	for _, s := range syms {
		if symbolConflictKinds[s.Kind] && (s.Receiver != "" || langparse.TypeKinds[s.Kind] || s.Kind == "type") {
			k := idKey{s.Name, s.Kind, s.Receiver, s.Outer, strings.Join(s.Params, "\x00")}
			if seen[k] {
				dropped = append(dropped, s)
				continue
			}
			seen[k] = true
		}
		kept = append(kept, s)
	}
	return kept, dropped
}

// disambiguateSameHeaderImpls suffixes the Outer discriminator of repeated
// same-header Rust impl blocks so each block keeps a distinct identity:
// several `impl Foo {}` blocks in one file are legal Rust, but their
// normalized headers (the discriminator, langparse.ImplIdentity) are
// identical, which would trip the fail-loud duplicate gate below. The first
// block keeps its header; the Nth becomes "<header>#N". Only kind "impl" is
// touched: duplicate method names under same-type inherent impls are
// illegal Rust (E0592/E0119), so methods never need disambiguation. Outer
// never joins FullName (impl_identity.go contract), so graph keys, node
// identity and the parity contract are unchanged. Deterministic per file:
// applyFileResult folds results in lexical file order and both extractors
// (tree-sitter and regex) emit symbols in document order.
func disambiguateSameHeaderImpls(syms []Symbol) {
	seen := map[string]int{}
	for i := range syms {
		if syms[i].Kind != "impl" {
			continue
		}
		key := syms[i].Name + "\x00" + syms[i].Outer
		seen[key]++
		if n := seen[key]; n > 1 {
			syms[i].Outer = fmt.Sprintf("%s#%d", syms[i].Outer, n)
		}
	}
}

// checkFileSymbolConflicts fails loudly when one file's extracted symbols
// collide: the graph keys nodes by FullName, so two symbols sharing (name,
// kind, receiver) on different lines are the SAME node defined twice.
// Different receivers (A.list vs B.list) are distinct nodes, left to the
// ambiguity machinery. Sorts indices — never the symbol slice — O(n log n). Only type kinds and receiver-bearing symbols fail loud: scoped locals (const/var/func, empty receiver), shell re-assignment, data-file props and Go's `_` legitimately repeat.
func checkFileSymbolConflicts(rel string, syms []Symbol) error {
	if len(syms) < 2 {
		return nil
	}
	order := make([]int, len(syms))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		x, y := syms[a], syms[b]
		switch {
		case x.Name != y.Name:
			return strings.Compare(x.Name, y.Name)
		case x.Kind != y.Kind:
			return strings.Compare(x.Kind, y.Kind)
		case x.Receiver != y.Receiver:
			return strings.Compare(x.Receiver, y.Receiver)
		case x.Outer != y.Outer:
			return strings.Compare(x.Outer, y.Outer)
		default:
			return x.Line - y.Line
		}
	})
	for i := 1; i < len(order); i++ {
		p, c := syms[order[i-1]], syms[order[i]]
		// Equal Params/Outer keeps the conflict (true duplicate); different
		// = an overload (WD-6) or a nested type under another parent (WD-6c).
		if p.Name == c.Name && p.Kind == c.Kind && p.Receiver == c.Receiver && p.Outer == c.Outer &&
			(p.Line != c.Line || p.End != c.End) && symbolConflictKinds[c.Kind] && (c.Receiver != "" || langparse.TypeKinds[c.Kind] || c.Kind == "type") &&
			slices.Equal(p.Params, c.Params) {
			return fmt.Errorf("index: duplicate symbol conflict in %s: %q (%s) at lines %d and %d — the same identity was extracted twice; refusing to index (fail-loud; report an extractor bug)", rel, c.Name, c.Kind, p.Line, c.Line)
		}
	}
	return nil
}

// Callers only iterate the result — the cached slices are shared.
func (ix *Index) symbolsFor(name string) []Symbol {
	// The name -> symbols table is built eagerly by the build/update paths
	// (buildSymbolIndex) and lazily on the first lookup for indexes that
	// loaded without it. The sync.Once makes the deferred build safe under
	// concurrent readers; the table is derived from the immutable symbol
	// table, so the eager and deferred paths produce identical results.
	c := ix.getCache()
	c.symbolOnce.Do(func() {
		c.symbolIdx = buildSymbolTable(ix.Symbols)
	})
	return c.symbolIdx[name]
}

// buildSymbolTable computes the name -> symbols map that turns symbolsFor
// from a full-index linear scan into a map lookup. Per-name slice order
// matches the linear scan's (append in Symbols order), so results are
// identical.
func buildSymbolTable(symbols []Symbol) map[string][]Symbol {
	m := make(map[string][]Symbol, len(symbols)*2)
	for _, s := range symbols {
		m[s.Name] = append(m[s.Name], s)
		if fn := s.FullName(); fn != s.Name {
			m[fn] = append(m[fn], s)
		}
	}
	// Canonical candidate order (file, line, kind, name): resolveName and the
	// other defs[0] consumers must not depend on insertion order, which a
	// delta-written store changes relative to a full rebuild.
	for _, list := range m {
		sort.Slice(list, func(i, j int) bool { return symbolLess(list[i], list[j]) })
	}
	return m
}

func symbolLess(a, b Symbol) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Name < b.Name
}

// buildSymbolIndex precomputes the name -> symbols map that turns
// symbolsFor from a full-index linear scan into a map lookup. Called by
// the build paths before the finalize passes (computeCallers and
// addDispatchEdges call symbolsFor per call edge, which made finalize
// O(edges x symbols) — the dominant build cost on symbol-heavy repos) and
// by the SQLite store before its load-time computeCallers. The sync.Once
// makes it idempotent — a Load that skipped the eager pass has the table
// built on the first query instead — and safe under concurrent first
// access.
func (ix *Index) buildSymbolIndex() {
	c := ix.getCache()
	c.symbolOnce.Do(func() {
		c.symbolIdx = buildSymbolTable(ix.Symbols)
	})
}

// FindSymbol returns the first symbol matching name, exact on Name or
// FullName ("Type.Method"). ok is false when nothing matches.
func (ix *Index) FindSymbol(name string) (Symbol, bool) {
	if defs := ix.symbolsFor(name); len(defs) > 0 {
		return defs[0], true
	}
	return Symbol{}, false
}

// IsGenerated reports whether the file at root-relative path was marked as
// tool-generated at index time (path convention or a "Code generated" banner
// in its head). It is a ranking hint, not a hard claim.
func (ix *Index) IsGenerated(rel string) bool {
	return ix.GeneratedFiles != nil && ix.GeneratedFiles[rel]
}

// ResolveName finds a definition for a call target. Exact matches win; a
// package-qualified target like "index.Build" falls back to the bare name
// ("Build") so call sites still resolve to real definitions.
func (ix *Index) ResolveName(name string) (Symbol, bool) {
	return resolveName(ix, name)
}

// Search matches symbols by pattern. Patterns support "*" wildcards and the
// prefixes "func ", "type ", "struct ", "method ", "const ", "var ", "call ".
// Kind-prefixed queries iterate the precomputed kind bucket (when the index
// has one) instead of every symbol; plain queries keep the full scan.
func (ix *Index) Search(pattern string, limit int) []Symbol {
	if limit <= 0 {
		limit = 50
	}
	re, kind := symbolRegex(pattern)
	if kind != "" {
		// Buckets preserve Symbols order and Search re-applies symbolMatches,
		// so results (and the limit cut) are identical to the linear scan.
		if syms, ok := ix.kindSymbols(kind); ok {
			var out []Symbol
			for _, s := range syms {
				if symbolMatches(s, kind, re) {
					out = append(out, s)
					if len(out) >= limit {
						break
					}
				}
			}
			return out
		}
	}
	var out []Symbol
	for _, s := range ix.Symbols {
		if symbolMatches(s, kind, re) {
			out = append(out, s)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

// kindSymbols returns the precomputed symbol bucket for a search kind
// ("func", "method", "type", "entry", ...). ok is false when the index has
// no kind index, and Search falls back to the linear scan — the
// pre-index behavior. The bucket is built on the first call (sync.Once) if
// the index loaded without one, so a kind-filtered Search pays one
// O(symbols) pass and then serves O(bucket) lookups.
func (ix *Index) kindSymbols(kind string) ([]Symbol, bool) {
	c := ix.getCache()
	c.kindOnce.Do(func() {
		c.kindIdx = buildKindTable(ix.Symbols)
	})
	syms, ok := c.kindIdx[kind]
	return syms, ok
}

// symbolRegexCacheSize bounds the number of compiled search regexes kept in
// memory. Search compiles one regex per query and the compile is pure
// overhead relative to the scan; without a cache, repeated patterns (the
// common case in agent loops) pay it every time.
const symbolRegexCacheSize = 128

// cachedSymbolRegex returns the compiled regex for a stripped search pattern,
// compiling and caching it on first use. Eviction is FIFO once the cache
// reaches symbolRegexCacheSize entries; evicting a regex never changes
// behavior — the same pattern is simply recompiled on its next use.
func cachedSymbolRegex(p string) *regexp.Regexp {
	// Fast path under the shared read lock: concurrent Search calls never
	// serialize on each other's cache lookups.
	symbolRegexCacheMu.RLock()
	re, ok := symbolRegexCache[p]
	symbolRegexCacheMu.RUnlock()
	if ok {
		return re
	}
	// Compile OUTSIDE the lock: a cold pattern's compile must not block
	// concurrent searches. Losing an insert race costs one redundant
	// compile; the double-check under the write lock below keeps the
	// cache canonical.
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$"
	compiled := regexp.MustCompile(expr)
	symbolRegexCacheMu.Lock()
	defer symbolRegexCacheMu.Unlock()
	if re, ok := symbolRegexCache[p]; ok {
		return re
	}
	if len(symbolRegexCache) >= symbolRegexCacheSize {
		delete(symbolRegexCache, symbolRegexOrder[0])
		symbolRegexOrder = symbolRegexOrder[1:]
	}
	symbolRegexCache[p] = compiled
	symbolRegexOrder = append(symbolRegexOrder, p)
	return compiled
}

// SearchPatternKind returns the kind prefix of a search pattern ("func" for
// "func handle*") or "" when the pattern has no recognized kind prefix.
// Kind prefixes restrict a Search to one symbol kind: `func handle*` matches
// only functions, while methods — a distinct kind — need `method handle*`.
func SearchPatternKind(pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i > 0 {
		switch pattern[:i] {
		case "func", "method", "struct", "interface", "type", "const", "var",
			"class", "enum", "trait", "module", "union", "impl", "prop", "heading", "entry":
			return pattern[:i]
		}
	}
	return ""
}

// KindGlobHint returns the kind-glob hint line for a NO-MATCH AST search
// whose pattern carried no kind prefix (6.4): a bare `handle*` silently
// misses methods (their kind is "method", not "func"), and `func handle*`
// never returns them either. Returns "" when the pattern already uses a kind
// prefix (or is empty) — the hint is deterministic and short.
func KindGlobHint(pattern string) string {
	if SearchPatternKind(pattern) != "" || strings.TrimSpace(pattern) == "" {
		return ""
	}
	return " — no matches: try a kind prefix (func|method|type|struct|interface|const|var|class), e.g. 'method handle*'"
}

func symbolRegex(pattern string) (*regexp.Regexp, string) {
	kind := SearchPatternKind(pattern)
	p := pattern
	if kind != "" {
		p = pattern[len(kind)+1:]
	}
	// The cache is keyed on the stripped pattern, so "func foo" and
	// "method foo" share one compiled regex; only the kind differs.
	return cachedSymbolRegex(p), kind
}

func symbolMatches(s Symbol, kind string, re *regexp.Regexp) bool {
	if kind == "entry" {
		return s.Entry && (re.MatchString(s.Name) || (s.Receiver != "" && re.MatchString(s.Receiver+"."+s.Name)) ||
			(s.Route != "" && re.MatchString(s.Route)))
	}
	if kind == "type" {
		// "type" is a super-category matching class, interface, enum, record,
		// struct, trait, and union kinds, not just the Go-specific "type" kind.
		if !searchTypeKinds[s.Kind] {
			return false
		}
	} else if kind != "" && s.Kind != kind {
		return false
	}
	return re.MatchString(s.Name) || (s.Receiver != "" && re.MatchString(s.Receiver+"."+s.Name)) ||
		(s.Route != "" && re.MatchString(s.Route))
}

// searchTypeKinds is the set of symbol kinds matched by the "type" search
// prefix. It is a superset of the parser's TypeKinds (which is used for call
// graph construction and must not include "type" or "record").
var searchTypeKinds = map[string]bool{
	"type": true, "class": true, "interface": true, "struct": true,
	"enum": true, "record": true, "trait": true, "union": true,
}
