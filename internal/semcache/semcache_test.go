package semcache

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

func TestSimilarity(t *testing.T) {
	if got := Similarity("hello world", "hello world"); got != 1 {
		t.Fatalf("identical should be 1, got %v", got)
	}
	if got := Similarity("", "anything"); got != 0 {
		t.Fatalf("empty should be 0, got %v", got)
	}
	// Near-duplicate phrasing shares most shingles.
	a := "the database connection failed during migration"
	b := "the database connection failed during the migration run"
	if got := Similarity(a, b); got < 0.5 {
		t.Fatalf("near-duplicate should be similar, got %v", got)
	}
	// Unrelated sentences are disjoint.
	c := "buy cheap luxury apartments in zurich"
	if got := Similarity(a, c); got > 0.3 {
		t.Fatalf("unrelated should be dissimilar, got %v", got)
	}
	// Word order does not matter (the whole point of semantic overlap).
	d := "during migration the database connection failed"
	if got := Similarity(a, d); got < 0.5 {
		t.Fatalf("reordered sentence should stay similar, got %v", got)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	_ = Store("prompt", "how do I compress a large server log?", "cached: dedupe timestamps")
	var v string
	// Genuine near-duplicate: shares enough shingles to clear the 0.60
	// DefaultThreshold (it must also clear the sim>=0.5 assertion below).
	matched, sim, hit, err := Lookup("prompt", "how do i compress a very large server log", &v, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatalf("expected a fuzzy hit, sim=%v", sim)
	}
	if v != "cached: dedupe timestamps" {
		t.Fatalf("wrong payload %q", v)
	}
	if matched == "" {
		t.Fatalf("expected matched input reported")
	}
	if sim < 0.5 {
		t.Fatalf("expected meaningful similarity, got %v", sim)
	}

	// Disjoint input must NOT hit, even though the cache is fuzzy.
	_ = Store("prompt", "how do I compress a large server log?", "x")
	var v2 string
	_, _, hit, err = Lookup("prompt", "buy a ticket to the opera", &v2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatalf("disjoint input should not hit")
	}

	// Identical input always hits with similarity 1.
	_, sim, hit, err = Lookup("prompt", "how do I compress a large server log?", &v2, 0)
	if err != nil || !hit || sim != 1 {
		t.Fatalf("identical input should hit with sim=1, hit=%v sim=%v err=%v", hit, sim, err)
	}
}

func TestNamespacesAreSeparate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	_ = Store("prompt", "panic in handler at line 42", "prompt-result")
	_ = Store("log", "panic in handler at line 42", "log-result")
	var v string
	if _, _, hit, _ := Lookup("prompt", "panic in handler line 42", &v, 0); !hit || v != "prompt-result" {
		t.Fatalf("prompt ns wrong: hit=%v v=%q", hit, v)
	}
	if _, _, hit, _ := Lookup("log", "panic in handler line 42", &v, 0); !hit || v != "log-result" {
		t.Fatalf("log ns wrong: hit=%v v=%q", hit, v)
	}
}

func TestIndexBoundedAndClearable(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	for i := 0; i < MaxEntries+50; i++ {
		_ = Store("bench", fmt.Sprintf("input number %d with distinctive words %d", i, i), "p")
	}
	ents, err := Entries("bench")
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) > MaxEntries {
		t.Fatalf("index not bounded: %d entries", len(ents))
	}
	stats, err := Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["bench"].Entries != len(ents) {
		t.Fatalf("stats mismatch: %v vs %d", stats["bench"].Entries, len(ents))
	}
	// 250 stores into a 200-entry cap: exactly 50 evictions, all accounted.
	if stats["bench"].Evictions != 50 {
		t.Fatalf("expected 50 evictions, got %d", stats["bench"].Evictions)
	}
	if err := Clear("bench"); err != nil {
		t.Fatal(err)
	}
	if n, _ := Entries("bench"); len(n) != 0 {
		t.Fatalf("clear left %d entries", len(n))
	}
}

func TestSignatureStable(t *testing.T) {
	words := make([]string, 3000)
	for i := range words {
		words[i] = "word" + strconv.Itoa(i)
	}
	big := strings.Join(words, " ")
	sa := shingles(big)
	sb := shingles(big)
	if len(sa) != len(sb) {
		t.Fatalf("signatures unstable: %d vs %d", len(sa), len(sb))
	}
	if len(sa) != MaxShingles {
		t.Fatalf("signature not capped: %d", len(sa))
	}
}

func BenchmarkShingles(b *testing.B) {
	text := "the quick brown fox jumps over the lazy dog and the database connection failed during migration"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = shingles(text)
	}
}

// TestLookupTTLRejectsStale: an entry stored past the TTL is a miss and is
// reclaimed from the index (and its payload), so a stale answer is never
// served. Entries without a usable timestamp (zero At — e.g. written before
// the At field existed) are stale by definition.
func TestLookupTTLRejectsStale(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_SEMCACHE_TTL", "") // force the 168h default
	_ = Clear("")
	_ = Store("ttl", "the database connection failed during migration", "fresh-payload")

	// Backdate the stored entry past the default TTL.
	st := lockFor("ttl")
	st.mu.Lock()
	es, _ := st.loadIndex("ttl")
	for i := range es {
		es[i].At = time.Now().Add(-200 * time.Hour)
	}
	_ = saveIndex("ttl", es)
	st.es = es
	st.mu.Unlock()

	// A near-duplicate lookup must treat the stale entry as a miss.
	var v string
	if _, _, hit, err := Lookup("ttl", "the database connection failed during the migration run", &v, 0); err != nil || hit {
		t.Fatalf("stale entry must be a miss: hit=%v err=%v", hit, err)
	}
	// And the stale entry is reclaimed.
	if ents, _ := Entries("ttl"); len(ents) != 0 {
		t.Fatalf("stale entry must be reclaimed, got %d entries", len(ents))
	}
	// A fresh store hits again.
	_ = Store("ttl", "the database connection failed during migration", "fresh2")
	if _, _, hit, _ := Lookup("ttl", "the database connection failed during the migration run", &v, 0); !hit || v != "fresh2" {
		t.Fatalf("fresh entry must hit after stale pruning: hit=%v v=%q", hit, v)
	}
}

// TestLookupTTLEnv drives the TTL from KERN_SEMCACHE_TTL: an entry stored
// under a 50ms TTL is a miss after it ages out.
func TestLookupTTLEnv(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_SEMCACHE_TTL", "50ms")
	_ = Clear("")
	_ = Store("ttl-env", "the database connection failed during migration", "p")
	time.Sleep(150 * time.Millisecond)
	var v string
	if _, _, hit, _ := Lookup("ttl-env", "the database connection failed during the migration run", &v, 0); hit {
		t.Fatal("entry past a 50ms TTL must be a miss")
	}
}

// TestStatsCounters: per-namespace hit/miss/savings accounting is reported by
// Stats: a hit and a miss each count once, a hit records the stored payload
// size as the savings estimate, and the entry count stays truthful.
func TestStatsCounters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	ns := "counters"
	_ = Store(ns, "the database connection failed during migration", "p")
	var v string
	if _, _, hit, _ := Lookup(ns, "the database connection failed during the migration run", &v, 0); !hit {
		t.Fatal("expected a hit")
	}
	if _, _, hit, _ := Lookup(ns, "buy a ticket to the opera tonight", &v, 0); hit {
		t.Fatal("expected a miss")
	}
	st, err := Stats()
	if err != nil {
		t.Fatal(err)
	}
	s := st[ns]
	if s.Hits != 1 || s.Misses != 1 {
		t.Fatalf("counters wrong: hits=%d misses=%d", s.Hits, s.Misses)
	}
	if s.SavedBytes <= 0 {
		t.Fatalf("expected saved bytes recorded on hit, got %d", s.SavedBytes)
	}
	if s.Entries != 1 {
		t.Fatalf("expected 1 entry, got %d", s.Entries)
	}
}

// TestLookupTouchRefreshesAt: a hit advances the matched entry's last-use At
// (throttled by touchPersistInterval) and persists it, so LRU eviction and
// idle-time TTL semantics survive restarts.
func TestLookupTouchRefreshesAt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	ns := "touch"
	_ = Store(ns, "the database connection failed during migration", "p")
	// Backdate the entry past the touch threshold (but inside the TTL).
	st := lockFor(ns)
	st.mu.Lock()
	es, _ := st.loadIndex(ns)
	es[0].At = time.Now().Add(-2 * time.Hour)
	st.es = es
	st.mu.Unlock()
	var v string
	if _, _, hit, _ := Lookup(ns, "the database connection failed during the migration run", &v, 0); !hit {
		t.Fatal("expected a hit")
	}
	st.mu.Lock()
	es, _ = st.loadIndex(ns)
	age := time.Since(es[0].At)
	st.mu.Unlock()
	if age > 90*time.Minute {
		t.Fatalf("hit did not refresh At (age %v)", age)
	}
}

// TestStatsAgeSweepReclaimsUntouchedNamespaces: Stats() sweeps every on-disk
// namespace, so a stale entry in a namespace no lookup ever touches is
// reclaimed instead of lingering forever.
func TestStatsAgeSweepReclaimsUntouchedNamespaces(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("KERN_SEMCACHE_TTL", "") // force the 168h default
	_ = Clear("")
	ns := "untouched"
	_ = Store(ns, "the database connection failed during migration", "p")
	// Backdate the entry past the TTL — but never Lookup this namespace.
	st := lockFor(ns)
	st.mu.Lock()
	es, _ := st.loadIndex(ns)
	es[0].At = time.Now().Add(-200 * time.Hour)
	st.es = es
	st.mu.Unlock()
	stats, err := Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats[ns].Entries != 0 {
		t.Fatalf("age sweep should reclaim the stale entry, got %d", stats[ns].Entries)
	}
	// The stale payload file is reclaimed too.
	payloadPath := cache.Path("data", "sem/"+ns+"/"+cache.Hash([]byte("the database connection failed during migration"))+".json")
	if _, err := os.Stat(payloadPath); err == nil {
		t.Fatalf("stale payload should be reclaimed by the sweep")
	}
}

func TestLockForStripesPerNamespace(t *testing.T) {
	a1, a2 := lockFor("striped-a"), lockFor("striped-a")
	b := lockFor("striped-b")
	if a1 != a2 {
		t.Fatal("same namespace must return the same lock state")
	}
	if a1 == b {
		t.Fatal("different namespaces must not share a lock state")
	}
}

// TestPersistedCountersCompoundAcrossRestarts: hit/miss counters are
// persisted to disk (sem/<ns>-counters, deliberately without a .json
// extension so the cache GC never evicts the accounting), so a fresh process
// — simulated here by resetting the in-memory state — still sees the
// accumulated numbers. This is what makes `kern cache` show a compounding
// hit rate instead of always-zero per-process atomics.
func TestPersistedCountersCompoundAcrossRestarts(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	ns := "persisted"
	_ = Store(ns, "the database connection failed during migration", "p")
	var v string
	if _, _, hit, _ := Lookup(ns, "the database connection failed during the migration run", &v, 0); !hit {
		t.Fatal("expected a hit")
	}
	// The counters write is throttled to once per second; let the hit's
	// write land before the miss so both increments reach the file.
	time.Sleep(1100 * time.Millisecond)
	if _, _, hit, _ := Lookup(ns, "buy a ticket to the opera tonight", &v, 0); hit {
		t.Fatal("expected a miss")
	}

	// The counters file exists with both increments.
	b, err := os.ReadFile(countersPath(ns))
	if err != nil {
		t.Fatalf("counters file missing: %v", err)
	}
	var c nsCounters
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("counters file corrupt: %v", err)
	}
	if c.Hits != 1 || c.Misses != 1 {
		t.Fatalf("persisted counters wrong: %+v", c)
	}
	// The counters file must not be a .json (the cache GC / kern cache walk
	// would treat it as a cache entry).
	if _, err := os.Stat(countersPath(ns) + ".json"); err == nil {
		t.Fatal("counters file must not carry a .json extension")
	}

	// Simulate a fresh process: drop the in-memory index and counters.
	st := lockFor(ns)
	st.mu.Lock()
	st.es = nil
	st.pc = nil
	st.mu.Unlock()

	nss, err := Namespaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(nss) != 1 || nss[0].Namespace != ns {
		t.Fatalf("Namespaces = %+v, want exactly the persisted namespace", nss)
	}
	if nss[0].Hits != 1 || nss[0].Misses != 1 {
		t.Fatalf("restart view lost persisted counters: %+v", nss[0])
	}
	if nss[0].Entries != 1 || nss[0].Bytes <= 0 {
		t.Fatalf("restart view lost index shape: %+v", nss[0])
	}
}

// TestNamespacesEmptyWithoutSemDir: a cache with no semantic data yields an
// empty report (no error) — the `kern cache` renderer prints the single
// "no entries yet" line instead of a zero-table.
func TestNamespacesEmptyWithoutSemDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	nss, err := Namespaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(nss) != 0 {
		t.Fatalf("fresh cache must have no namespaces, got %+v", nss)
	}
}

// TestClearResetsPersistedCounters: clearing a namespace wipes its counters
// file (and in-memory counter state), so a cleared cache starts its
// accounting fresh instead of compounding across clears.
func TestClearResetsPersistedCounters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	ns := "clear-counters"
	_ = Store(ns, "the database connection failed during migration", "p")
	var v string
	if _, _, hit, _ := Lookup(ns, "the database connection failed during the migration run", &v, 0); !hit {
		t.Fatal("expected a hit")
	}
	if err := Clear(ns); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(countersPath(ns)); err == nil {
		t.Fatal("counters file must be removed by Clear")
	}
	// The namespace is gone from the report entirely (no index file).
	for _, s := range mustNamespaces(t) {
		if s.Namespace == ns {
			t.Fatalf("cleared namespace still reported: %+v", s)
		}
	}
	// A new lookup starts the counters fresh (miss=1, hit=0) and the first
	// write lands immediately (the throttle timestamp was reset by Clear).
	if _, _, hit, _ := Lookup(ns, "buy a ticket to the opera tonight", &v, 0); hit {
		t.Fatal("expected a miss on the cleared namespace")
	}
	b, err := os.ReadFile(countersPath(ns))
	if err != nil {
		t.Fatalf("counters file not recreated: %v", err)
	}
	var c nsCounters
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("counters file corrupt: %v", err)
	}
	if c.Hits != 0 || c.Misses != 1 {
		t.Fatalf("counters did not restart fresh: %+v", c)
	}
}

// mustNamespaces is a test helper that fails the test on a Namespaces error.
func mustNamespaces(t *testing.T) []NamespaceStat {
	t.Helper()
	nss, err := Namespaces()
	if err != nil {
		t.Fatal(err)
	}
	return nss
}

// TestNamespacesDoNotSerialize proves the striped locks let one namespace's
// critical section proceed while another namespace's lock is held — the
// property the old single process-wide mutex violated.
func TestNamespacesDoNotSerialize(t *testing.T) {
	a, b := lockFor("serialize-a"), lockFor("serialize-b")
	a.mu.Lock()
	acquiredB := make(chan struct{})
	go func() {
		b.mu.Lock()
		close(acquiredB)
		b.mu.Unlock()
	}()
	select {
	case <-acquiredB:
		a.mu.Unlock()
	case <-time.After(2 * time.Second):
		a.mu.Unlock()
		t.Fatal("namespace B blocked while namespace A's lock was held: locks are not striped")
	}
}

func TestNamespaceIsolation(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	_ = Store("nsA", "the database connection failed during migration", "A")
	_ = Store("nsB", "the queue worker crashed while processing jobs", "B")
	var v string
	if _, _, hit, _ := Lookup("nsA", "the database connection failed during the migration run", &v, 0); !hit || v != "A" {
		t.Fatalf("nsA hit wrong: hit=%v v=%q", hit, v)
	}
	if _, _, hit, _ := Lookup("nsB", "the queue worker crashed while processing the job", &v, 0); !hit || v != "B" {
		t.Fatalf("nsB hit wrong: hit=%v v=%q", hit, v)
	}
	// Cross-namespace contamination: each namespace's near-duplicate must not
	// hit in the other (disjoint stored inputs).
	if _, _, hit, _ := Lookup("nsB", "the database connection failed during the migration run", &v, 0); hit {
		t.Fatalf("nsB served an nsA payload: %q", v)
	}
	if _, _, hit, _ := Lookup("nsA", "the queue worker crashed while processing the job", &v, 0); hit {
		t.Fatalf("nsA served an nsB payload: %q", v)
	}
	// Both namespaces remain independently usable after the cross checks.
	_ = Store("nsA", "the server is running out of disk space", "A2")
	if _, _, hit, _ := Lookup("nsA", "the server is almost out of disk space", &v, 0); !hit || v != "A2" {
		t.Fatalf("nsA second entry wrong: hit=%v v=%q", hit, v)
	}
	if _, _, hit, _ := Lookup("nsB", "the queue worker crashed while processing the job", &v, 0); !hit || v != "B" {
		t.Fatalf("nsB regressed after nsA stores: hit=%v v=%q", hit, v)
	}
}

// TestConcurrentCrossNamespaceLookups hammers Lookup/Store on two namespaces
// from 8 goroutines. Run with -race: it proves the striped locks introduce no
// data races and that namespaces do not cross-contaminate under concurrency.
func TestConcurrentCrossNamespaceLookups(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_ = Clear("")
	_ = Store("alpha", "the database connection failed during migration", "alpha-result")
	_ = Store("beta", "the queue worker crashed while processing jobs", "beta-result")
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				ns, want := "alpha", "alpha-result"
				if g%2 == 1 {
					ns, want = "beta", "beta-result"
				}
				// A hit must return this namespace's own payload.
				var v string
				matched, sim, hit, err := Lookup(ns, "the database connection failed during the migration run", &v, 0)
				if err != nil {
					t.Errorf("goroutine %d: %s lookup error: %v", g, ns, err)
					continue
				}
				if hit && v != want {
					t.Errorf("goroutine %d: %s returned wrong payload %q (matched %q, sim %v)", g, ns, v, matched, sim)
				}
				// Mix in writes so Store's payload-I/O-outside-lock path is
				// exercised concurrently.
				_ = Store(ns, fmt.Sprintf("goroutine %d iteration %d processing input", g, i), "p")
			}
		}(g)
	}
	wg.Wait()
	// Sanity: both namespaces still serve their own payloads afterwards.
	var v string
	if _, _, hit, _ := Lookup("alpha", "the database connection failed during the migration run", &v, 0); !hit || v != "alpha-result" {
		t.Fatalf("alpha lost its payload after concurrency: hit=%v v=%q", hit, v)
	}
	if _, _, hit, _ := Lookup("beta", "the queue worker crashed while processing jobs", &v, 0); !hit || v != "beta-result" {
		t.Fatalf("beta lost its payload after concurrency: hit=%v v=%q", hit, v)
	}
}
