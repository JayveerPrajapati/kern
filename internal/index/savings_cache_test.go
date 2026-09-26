package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// TestTokenSavingsForGraphCacheSameResult pins that TokenSavingsForGraph
// returns identical results whether the definition file's token count comes
// from the cache (a repeated call) or is computed fresh (the direct
// computeTokenSavings oracle, which never touches the cache).
func TestTokenSavingsForGraphCacheSameResult(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"def.go": `package demo

// Store keeps values.
type Store struct {
	items map[string]int
}

func NewStore() *Store {
	return &Store{items: map[string]int{}}
}

func (s *Store) Put(k string, v int) {
	s.items[k] = v
}

func (s *Store) Get(k string) (int, bool) {
	v, ok := s.items[k]
	return v, ok
}
`,
	})
	ix := New(dir)
	rel := "def.go"
	compact := "symbols: NewStore, Store.Put, Store.Get\ndef: def.go:5\ntokens: 400 -> 120 (70% saved)"

	cold := ix.TokenSavingsForGraph(rel, compact) // cache miss: reads + tokenizes the file
	warm := ix.TokenSavingsForGraph(rel, compact) // cache hit: same (path, mtime)
	oracle := computeTokenSavings(readFileForTest(t, filepath.Join(dir, rel)), compact, "graph")

	for name, got := range map[string]TokenStats{"cold": cold, "warm": warm} {
		if got != oracle {
			t.Fatalf("%s TokenSavingsForGraph = %+v, want %+v (uncached oracle)", name, got, oracle)
		}
	}
}

// TestTokenSavingsForGraphCacheInvalidatesOnMtime pins the invalidation
// contract: the cache is keyed by (path, mtime), so a content change that
// bumps the mtime is observed on the next query, while a content change that
// leaves the mtime untouched is served stale (the same trust model as
// reuseByMtime).
func TestTokenSavingsForGraphCacheInvalidatesOnMtime(t *testing.T) {
	dir := t.TempDir()
	rel := "def.go"
	p := filepath.Join(dir, rel)
	ix := New(dir)

	// Content A: small file, few tokens.
	contentA := "package demo\nfunc A() {}\n"
	if err := os.WriteFile(p, []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, base, base); err != nil {
		t.Fatal(err)
	}
	gotA := ix.TokenSavingsForGraph(rel, "compact graph A")
	if want := tokenize.Count(contentA); gotA.FullContext != want {
		t.Fatalf("after write A: FullContext = %d, want %d", gotA.FullContext, want)
	}

	// Content B: much larger file, more tokens. Bump the mtime explicitly so
	// the invalidation does not depend on filesystem mtime granularity.
	contentB := "package demo\n" + longGoFileBody
	if err := os.WriteFile(p, []byte(contentB), 0o644); err != nil {
		t.Fatal(err)
	}
	mtimeB := base.Add(2 * time.Second)
	if err := os.Chtimes(p, mtimeB, mtimeB); err != nil {
		t.Fatal(err)
	}
	gotB := ix.TokenSavingsForGraph(rel, "compact graph B")
	if want := tokenize.Count(contentB); gotB.FullContext != want {
		t.Fatalf("after write B + mtime bump: FullContext = %d, want %d", gotB.FullContext, want)
	}
	if gotB.FullContext == gotA.FullContext {
		t.Fatalf("test fixture degenerate: contents A and B tokenize equally (%d)", gotA.FullContext)
	}

	// Content C with the mtime restored to B's: the cache must serve the
	// stale B count (mtime unchanged => key unchanged).
	contentC := "package demo\nfunc C() {}\n" + longGoFileBody
	if err := os.WriteFile(p, []byte(contentC), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtimeB, mtimeB); err != nil {
		t.Fatal(err)
	}
	if got := ix.TokenSavingsForGraph(rel, "compact graph C"); got.FullContext != gotB.FullContext {
		t.Fatalf("content changed but mtime unchanged: FullContext = %d, want stale %d (mtime-keyed cache)", got.FullContext, gotB.FullContext)
	}

	// Finally bump the mtime: the new content must be picked up.
	mtimeC := mtimeB.Add(2 * time.Second)
	if err := os.Chtimes(p, mtimeC, mtimeC); err != nil {
		t.Fatal(err)
	}
	if got := ix.TokenSavingsForGraph(rel, "compact graph C"); got.FullContext != tokenize.Count(contentC) {
		t.Fatalf("after mtime bump: FullContext = %d, want %d", got.FullContext, tokenize.Count(contentC))
	}
}

// TestFileTokenCountBounded pins that the memo never grows past the size cap:
// distinct files keep evicting older entries rather than growing unboundedly.
func TestFileTokenCountBounded(t *testing.T) {
	dir := t.TempDir()
	const n = savingsCacheCap + 32
	for i := 0; i < n; i++ {
		letter := string(rune('a' + i%savingsCacheCap))
		p := filepath.Join(dir, "f"+letter+".go")
		content := "package demo\nfunc F" + letter + "(x int) int { return x + " + string(rune('0'+i%10)) + " }\n"
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := fileTokenCount(p); got != tokenize.Count(content) {
			t.Fatalf("fileTokenCount(%s) = %d, want %d", p, got, tokenize.Count(content))
		}
	}
	fileTokenSavingsCache.mu.Lock()
	size := len(fileTokenSavingsCache.entries)
	fileTokenSavingsCache.mu.Unlock()
	if size > savingsCacheCap {
		t.Fatalf("cache grew to %d entries, cap is %d", size, savingsCacheCap)
	}
}

// readFileForTest returns the file's content, failing the test on error.
// (Named to avoid clashing with index/watch.go's readFile.)
func readFileForTest(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// longGoFileBody is a token-rich Go body used to make content B/C tokenize
// very differently from the tiny content A.
const longGoFileBody = `// Store keeps values with a bounded cache and an eviction policy.
type Store struct {
	items  map[string]int
	order  []string
	limit  int
	misses int64
	hits   int64
}

// NewStore returns a Store that keeps at most limit entries.
func NewStore(limit int) *Store {
	return &Store{
		items: map[string]int{},
		order: make([]string, 0, limit),
		limit: limit,
	}
}

// Put inserts k with value v, evicting the least recently used entry when the
// store is at capacity.
func (s *Store) Put(k string, v int) {
	if _, ok := s.items[k]; !ok {
		s.order = append(s.order, k)
	}
	s.items[k] = v
	if len(s.order) > s.limit {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.items, oldest)
	}
}

// Get returns the value for k and whether it was present, recording a hit or
// a miss for the diagnostics counters.
func (s *Store) Get(k string) (int, bool) {
	v, ok := s.items[k]
	if ok {
		s.hits++
	} else {
		s.misses++
	}
	return v, ok
}
`
