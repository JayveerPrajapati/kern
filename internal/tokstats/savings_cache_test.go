package tokstats

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// writeTree writes a deterministic file tree to a temp dir (the index test
// helper this family's tests share; kept local so tokstats stays independent
// of index's test-only helpers).
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

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
	rel := "def.go"
	compact := "symbols: NewStore, Store.Put, Store.Get\ndef: def.go:5\ntokens: 400 -> 120 (70% saved)"

	// The oracle passes the same single-file baseline TokenSavingsForGraph
	// labels, so the full TokenStats (counts AND the denominator label) must
	// match whether the count came from the cache or a fresh read.
	cold := TokenSavingsForGraph(dir, []string{rel}, compact) // cache miss: reads + tokenizes the file
	warm := TokenSavingsForGraph(dir, []string{rel}, compact) // cache hit: same (path, mtime)
	oracle := ComputeTokenSavings(readFileForTest(t, filepath.Join(dir, rel)), compact, "graph", "1 file read raw")

	for name, got := range map[string]TokenStats{"cold": cold, "warm": warm} {
		if got != oracle {
			t.Fatalf("%s TokenSavingsForGraph = %+v, want %+v (uncached oracle)", name, got, oracle)
		}
	}
	if cold.Baseline != "1 file read raw" {
		t.Errorf("Baseline = %q, want %q (single-file denominator must be labeled)", cold.Baseline, "1 file read raw")
	}
}

// TestTokenSavingsForGraphCacheInvalidatesOnMtime pins the invalidation
// contract: the cache is keyed by (path, mtime, size), so a content change
// that bumps the mtime OR changes the size is observed on the next query.
// A content change that leaves BOTH untouched is served stale (the same
// trust model as reuseByMtime — V2/B2 residual, cosmetic footer only).
func TestTokenSavingsForGraphCacheInvalidatesOnMtime(t *testing.T) {
	dir := t.TempDir()
	rel := "def.go"
	p := filepath.Join(dir, rel)

	// Content A: small file, few tokens.
	contentA := "package demo\nfunc A() {}\n"
	if err := os.WriteFile(p, []byte(contentA), 0o644); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(p, base, base); err != nil {
		t.Fatal(err)
	}
	gotA := TokenSavingsForGraph(dir, []string{rel}, "compact graph A")
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
	gotB := TokenSavingsForGraph(dir, []string{rel}, "compact graph B")
	if want := tokenize.Count(contentB); gotB.FullContext != want {
		t.Fatalf("after write B + mtime bump: FullContext = %d, want %d", gotB.FullContext, want)
	}
	if gotB.FullContext == gotA.FullContext {
		t.Fatalf("test fixture degenerate: contents A and B tokenize equally (%d)", gotA.FullContext)
	}

	// Content C with the mtime restored to B's but a different SIZE: the
	// size half of the key must invalidate the stale B count (V2: mtime-only
	// caching served stale here).
	contentC := "package demo\nfunc C() {}\n" + longGoFileBody
	if err := os.WriteFile(p, []byte(contentC), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtimeB, mtimeB); err != nil {
		t.Fatal(err)
	}
	if got := TokenSavingsForGraph(dir, []string{rel}, "compact graph C"); got.FullContext != tokenize.Count(contentC) {
		t.Fatalf("content changed, mtime unchanged, size changed: FullContext = %d, want %d (size-keyed invalidation)", got.FullContext, tokenize.Count(contentC))
	}

	// Finally bump the mtime: the new content must be picked up.
	mtimeC := mtimeB.Add(2 * time.Second)
	if err := os.Chtimes(p, mtimeC, mtimeC); err != nil {
		t.Fatal(err)
	}
	if got := TokenSavingsForGraph(dir, []string{rel}, "compact graph C"); got.FullContext != tokenize.Count(contentC) {
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
