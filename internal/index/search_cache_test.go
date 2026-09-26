package index

// Tests pinning that the Search performance changes (regex cache + kind
// index) are behavior-neutral: results must be byte-identical to the
// pre-change linear scan, on both built and hand-constructed indexes.

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// searchReference replicates the pre-change Search exactly: per-query regex
// compile + linear scan over every symbol. Used as the oracle.
func searchReference(ix *Index, pattern string, limit int) []Symbol {
	if limit <= 0 {
		limit = 50
	}
	p := pattern
	kind := ""
	if i := strings.IndexByte(p, ' '); i > 0 {
		prefix := p[:i]
		switch prefix {
		case "func", "method", "struct", "interface", "type", "const", "var",
			"class", "enum", "trait", "module", "union", "impl", "prop", "heading", "entry":
			kind = prefix
			p = p[i+1:]
		}
	}
	expr := "^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `.*`) + "$"
	re := regexp.MustCompile(expr)
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

func assertSameResults(t *testing.T, ix *Index, pattern string, limit int) {
	t.Helper()
	want := searchReference(ix, pattern, limit)
	got := ix.Search(pattern, limit)
	if len(got) != len(want) {
		t.Fatalf("Search(%q, %d): len %d, want %d", pattern, limit, len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Kind != w.Kind || g.Name != w.Name || g.Receiver != w.Receiver ||
			g.File != w.File || g.Line != w.Line || g.Entry != w.Entry ||
			g.Route != w.Route || g.Framework != w.Framework || g.Lang != w.Lang ||
			!equalStrings(g.Params, w.Params) || !equalStrings(g.Returns, w.Returns) {
			t.Fatalf("Search(%q, %d)[%d] = %+v, want %+v", pattern, limit, i, g, w)
		}
	}
}

// searchBattery covers every kind prefix, wildcards, empty pattern, unknown
// prefixes, multi-space patterns and route/entry queries.
var searchBattery = []string{
	"", "*", "greet", "helper", "User", "User.Login", "*ser*", "*",
	"func", "func greet", "func *", "func  New", "method *login*",
	"method greet", "struct *", "struct User", "interface *", "type *",
	"type User", "const *", "var *", "class *", "class CartService",
	"entry */admin*", "entry *", "notaprefix foo", "func *Cache*",
	"func ", "entry", "type",
}

func TestSearchKindIndexResultsIdentical(t *testing.T) {
	files := map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
		"app.py": `from flask import Flask
app = Flask(__name__)
@app.route("/admin/users")
def admin_users():
    return "x"
`,
		"app.js": `export class CartService {
  constructor() {}
  checkout() {}
}
const helper = () => 1;
`,
	}
	ix := buildTestIndex(t, files)
	if ix.kindIdx == nil {
		t.Fatal("expected kind index to be built by Build")
	}
	for _, pattern := range searchBattery {
		for _, limit := range []int{1, 2, 5, 50, -1, 0, 1000} {
			assertSameResults(t, ix, pattern, limit)
		}
	}
}

// TestSearchKindIndexFallbackHandBuilt pins the nil-kindIdx fallback: an
// Index struct built by hand (never through Build/Load/reindexByFile) must
// behave exactly like the linear scan.
func TestSearchKindIndexFallbackHandBuilt(t *testing.T) {
	ix := &Index{
		Root:    "handbuilt",
		Version: indexVersion,
		Symbols: []Symbol{
			{Kind: "func", Name: "main", File: "a.go", Line: 1},
			{Kind: "method", Name: "Login", Receiver: "User", File: "a.go", Line: 5},
			{Kind: "struct", Name: "User", File: "a.go", Line: 4},
			{Kind: "entry", Name: "admin_users", Entry: true, Route: "/admin/users", File: "app.py", Line: 3},
			{Kind: "const", Name: "Limit", File: "b.go", Line: 2},
			{Kind: "var", Name: "counter", File: "b.go", Line: 3},
		},
	}
	if ix.kindIdx != nil {
		t.Fatal("hand-built index must start without a kind index")
	}
	for _, pattern := range searchBattery {
		for _, limit := range []int{1, 5, 50} {
			assertSameResults(t, ix, pattern, limit)
		}
	}
}

func TestSymbolRegexCacheBehavior(t *testing.T) {
	// Repeated pattern: same cached entry, identical match behavior.
	re1, _ := symbolRegex("func greet")
	re2, _ := symbolRegex("func greet")
	if re1 != re2 {
		t.Fatal("expected the same compiled regex for a repeated pattern")
	}
	// Different kind prefixes over the same stripped pattern share the entry
	// (cache is keyed on the stripped pattern); only the kind differs.
	re3, kind3 := symbolRegex("method greet")
	if re3 != re2 {
		t.Fatal("expected kind prefixes to share the stripped-pattern regex")
	}
	if kind3 != "method" {
		t.Fatalf("kind = %q, want method", kind3)
	}
	// Match behavior must be identical to a fresh compile.
	fresh := regexp.MustCompile(`^greet$`)
	for _, name := range []string{"greet", "greeting", "xgreet", "greetx", ""} {
		if got, want := re2.MatchString(name), fresh.MatchString(name); got != want {
			t.Fatalf("cached regex match(%q) = %v, fresh compile = %v", name, got, want)
		}
	}

	// Eviction: more than symbolRegexCacheSize distinct patterns must not grow
	// the cache unboundedly, and evicted patterns must still compile correctly
	// on their next use (identical match results to a fresh compile).
	for i := 0; i < symbolRegexCacheSize+64; i++ {
		symbolRegex(fmt.Sprintf("func evict%04d", i))
	}
	if len(symbolRegexCache) > symbolRegexCacheSize {
		t.Fatalf("cache grew to %d entries, want <= %d", len(symbolRegexCache), symbolRegexCacheSize)
	}
	if len(symbolRegexCache) != len(symbolRegexOrder) {
		t.Fatalf("cache map (%d) and order slice (%d) out of sync", len(symbolRegexCache), len(symbolRegexOrder))
	}
	for i := 0; i < symbolRegexCacheSize+64; i++ {
		pat := fmt.Sprintf("func evict%04d", i)
		re, kind := symbolRegex(pat)
		if kind != "func" {
			t.Fatalf("kind = %q, want func", kind)
		}
		want := regexp.MustCompile(`^evict` + fmt.Sprintf("%04d", i) + `$`)
		if re.MatchString(fmt.Sprintf("evict%04d", i)) != want.MatchString(fmt.Sprintf("evict%04d", i)) {
			t.Fatalf("pattern %q: cached regex diverged from fresh compile", pat)
		}
	}
}

// TestSearchConcurrent exercises the cache mutex and shared regexes under
// parallel Search calls (run with -race in CI).
func TestSearchConcurrent(t *testing.T) {
	files := map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	}
	ix := buildTestIndex(t, files)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				pattern := searchBattery[(g+i)%len(searchBattery)]
				if _, kind := symbolRegex(pattern); kind == "" {
					continue
				}
				ix.Search(pattern, 5)
			}
		}(g)
	}
	wg.Wait()
}
