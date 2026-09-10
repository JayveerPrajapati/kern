package semcache

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
		_ = Store("bench", "input number "+string(rune('a'+i%26))+" with words "+string(rune('z'-i%26)), "p")
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
	if stats["bench"] != len(ents) {
		t.Fatalf("stats mismatch: %v vs %d", stats["bench"], len(ents))
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
