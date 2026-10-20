package docsearch

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// seedDocIndex builds and persists a doc index for root in the (per-test
// isolated) cache dir — the on-disk state a previous `kern docs index`
// leaves behind.
func seedDocIndex(t *testing.T, root string) {
	t.Helper()
	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
}

// countLoads swaps the cachedLoad seam (the Load indirection LoadCached
// uses) for a counting wrapper and returns a func reading the running count.
// The original is restored via t.Cleanup, so an early t.Fatal cannot leak
// the swap into later tests. Direct Load calls (MergeFetched's internal
// load, Save) are not counted — only decodes flowing through the cache's
// load path, which is exactly the reload behavior the doc layer observes.
func countLoads(t *testing.T) func() int32 {
	t.Helper()
	orig := cachedLoad
	var n int32
	cachedLoad = func(root string) *Index {
		atomic.AddInt32(&n, 1)
		return orig(root)
	}
	t.Cleanup(func() { cachedLoad = orig })
	return func() int32 { return atomic.LoadInt32(&n) }
}

// TestDocCacheSecondSearchSkipsReload pins the cache's core contract: two
// sequential loads with an unchanged persisted index file decode the index
// from disk exactly ONCE — the second load is served from the in-process
// cache without calling Load again, returning the same decoded instance
// (behavior-preserving).
func TestDocCacheSecondSearchSkipsReload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Widget Guide\n\nThis guide describes widget configuration, deployment and maintenance steps in detail.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDocIndex(t, root)

	loads := countLoads(t)

	ix1, err := LoadCached(root)
	if err != nil {
		t.Fatalf("first LoadCached: %v", err)
	}
	if got := loads(); got != 1 {
		t.Fatalf("first LoadCached: Load called %d times, want 1", got)
	}

	ix2, err := LoadCached(root)
	if err != nil {
		t.Fatalf("second LoadCached: %v", err)
	}
	if got := loads(); got != 1 {
		t.Fatalf("second LoadCached with unchanged index file: Load called %d times, want still 1 (cache hit)", got)
	}
	if ix2 != ix1 {
		t.Error("second LoadCached returned a different index instance — the cache must serve the decoded instance from memory")
	}
}

// TestDocCacheIndexInvalidatesReload pins that an index write invalidates
// the cached entry: the next LoadCached re-loads from disk. The write path
// itself (IndexDir + Save — what doc.Index performs) must not call Load.
func TestDocCacheIndexInvalidatesReload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Widget Guide\n\nThis guide describes widget configuration, deployment and maintenance steps in detail.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDocIndex(t, root)

	loads := countLoads(t)

	if _, err := LoadCached(root); err != nil {
		t.Fatalf("first LoadCached: %v", err)
	}
	if _, err := LoadCached(root); err != nil {
		t.Fatalf("second LoadCached: %v", err)
	}
	if got := loads(); got != 1 {
		t.Fatalf("precondition: two loads on an unchanged index must not reload; got %d loads", got)
	}

	// The doc.Index write path: build in memory + persist. It must not
	// itself call Load.
	ix, err := IndexDir(root)
	if err != nil {
		t.Fatalf("IndexDir: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := loads(); got != 1 {
		t.Fatalf("IndexDir+Save itself must not call Load; got %d loads", got)
	}

	InvalidateCache(root)
	if _, err := LoadCached(root); err != nil {
		t.Fatalf("LoadCached after index: %v", err)
	}
	if got := loads(); got != 2 {
		t.Fatalf("LoadCached after InvalidateCache: Load called %d times, want 2 (re-load after invalidation)", got)
	}
}

// TestDocCacheFetchInvalidatesReload pins that action=fetch (which merges a
// new document into the persisted index via MergeFetched) invalidates the
// cached entry: the next LoadCached re-loads and sees the fetched document.
func TestDocCacheFetchInvalidatesReload(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Widget Guide\n\nThis guide describes widget configuration, deployment and maintenance steps in detail.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedDocIndex(t, root)

	loads := countLoads(t)

	if _, err := LoadCached(root); err != nil {
		t.Fatalf("first LoadCached: %v", err)
	}
	if _, err := LoadCached(root); err != nil {
		t.Fatalf("second LoadCached: %v", err)
	}
	if got := loads(); got != 1 {
		t.Fatalf("precondition: two loads on an unchanged index must not reload; got %d loads", got)
	}

	// The doc.Fetch write path: MergeFetched. Its internal Load is the
	// direct function, not the cache seam, so it must not bump the count.
	text := "Zephyrwidget reference: this document explains the zephyrwidget subsystem, its configuration flags and operational notes for production deployments.\n"
	added, err := MergeFetched(root, "kern-doc-cache-test-fetch", text)
	if err != nil {
		t.Fatalf("MergeFetched: %v", err)
	}
	if added == 0 {
		t.Fatal("MergeFetched added no chunks")
	}
	if got := loads(); got != 1 {
		t.Fatalf("MergeFetched itself must not call the cache seam's Load; got %d loads", got)
	}

	InvalidateCache(root)
	ix, err := LoadCached(root)
	if err != nil {
		t.Fatalf("LoadCached after fetch: %v", err)
	}
	if got := loads(); got != 2 {
		t.Fatalf("LoadCached after fetch + InvalidateCache: Load called %d times, want 2 (re-load after invalidation)", got)
	}
	if len(ix.Search("zephyrwidget", 5)) == 0 {
		t.Error("fetched document not searchable after invalidation")
	}
}

// TestDocCacheFingerprintTracksIndexFile pins the fingerprint contract: a
// missing index file yields "missing", a written file yields a stable
// fingerprint that changes when the file is rewritten, and a deleted file
// falls back to "missing".
func TestDocCacheFingerprintTracksIndexFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if fp := docFingerprint(root); fp != "missing" {
		t.Fatalf("fingerprint of absent index file = %q, want %q", fp, "missing")
	}

	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	fp1 := docFingerprint(root)
	if fp1 == "missing" {
		t.Fatal("fingerprint of written index file = missing")
	}
	if fp2 := docFingerprint(root); fp2 != fp1 {
		t.Fatalf("fingerprint changed without a rewrite: %q -> %q", fp1, fp2)
	}

	// An external rewrite of the index file must change the fingerprint.
	path := cache.Path("data", CacheKey(root)+".json")
	if err := os.WriteFile(path, []byte(`{"root":"x","docs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if fp3 := docFingerprint(root); fp3 == fp1 {
		t.Fatal("fingerprint unchanged after index file rewrite")
	}

	// A deleted file is "missing" again.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if fp := docFingerprint(root); fp != "missing" {
		t.Fatalf("fingerprint after delete = %q, want missing", fp)
	}
}

// TestDocCacheEntriesPinnedToFingerprint pins that a cached entry is served
// only while the index file still matches the fingerprint it was cached
// under — rewritten or deleted files fall through to a reload — and that the
// map is capped with the oldest entry evicted.
func TestDocCacheEntriesPinnedToFingerprint(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	// Isolate the package-level cache from entries other tests left behind.
	docCacheMu.Lock()
	savedCache, savedSeq := docCache, docCacheSeq
	docCache, docCacheSeq = map[string]docCacheEntry{}, 0
	docCacheMu.Unlock()
	defer func() {
		docCacheMu.Lock()
		docCache, docCacheSeq = savedCache, savedSeq
		docCacheMu.Unlock()
	}()

	ix, err := IndexDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	fp := docFingerprint(root)
	if fp == "missing" {
		t.Fatal("precondition: index file must exist")
	}

	docCachePut(root, fp, ix)
	if got := docCacheGet(root, fp); got != ix {
		t.Fatal("cached entry not returned for a matching fingerprint")
	}

	// Rewrite the file -> fingerprint changes -> entry no longer served.
	path := cache.Path("data", CacheKey(root)+".json")
	if err := os.WriteFile(path, []byte(`{"root":"x","docs":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := docCacheGet(root, docFingerprint(root)); got != nil {
		t.Fatal("stale entry served after the index file was rewritten")
	}

	// Delete the file -> "missing" -> entry must never be served.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := docCacheGet(root, docFingerprint(root)); got != nil {
		t.Fatal("entry served for a root whose index file was deleted")
	}

	// Cap: at capacity, the oldest entry is evicted. The real-root entry is
	// the oldest (inserted first), so the 17 fake puts trim it and the
	// first fake out, leaving exactly maxDocCacheEntries.
	for i := 0; i < maxDocCacheEntries+1; i++ {
		docCachePut(filepath.Join(root, "fake-root", strconv.Itoa(i)), "fp-"+strconv.Itoa(i), ix)
	}
	if len(docCache) != maxDocCacheEntries {
		t.Fatalf("cache size = %d, want cap %d", len(docCache), maxDocCacheEntries)
	}
	if _, ok := docCache[filepath.Join(root, "fake-root", "0")]; ok {
		t.Fatal("oldest entry not evicted at cap")
	}
	if _, ok := docCache[filepath.Join(root, "fake-root", strconv.Itoa(maxDocCacheEntries))]; !ok {
		t.Fatal("newest entry missing after eviction")
	}
}

// TestDocCacheConcurrentSearchesRaceFree hammers LoadCached from many
// goroutines (with a concurrent re-index) on one root; run with -race this
// proves the package-level cache is safe for concurrent tool calls.
func TestDocCacheConcurrentSearchesRaceFree(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for i := 0; i < 3; i++ {
		body := "# Guide\n\nParagraph with widget configuration notes for document number " + strconv.Itoa(i) + ".\n"
		if err := os.WriteFile(filepath.Join(root, "doc"+strconv.Itoa(i)+".md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seedDocIndex(t, root)

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if _, err := LoadCached(root); err != nil {
					errs <- fmt.Errorf("loadcached: %w", err)
					return
				}
			}
		}()
	}
	// A concurrent re-index (IndexDir + Save + InvalidateCache) exercises
	// invalidate + reload under the race detector.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			ix, err := IndexDir(root)
			if err != nil {
				errs <- fmt.Errorf("index: %w", err)
				return
			}
			if err := ix.Save(); err != nil {
				errs <- fmt.Errorf("save: %w", err)
				return
			}
			InvalidateCache(root)
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
