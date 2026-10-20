package filecache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// writeFile writes content to a fresh file under dir and returns its
// absolute path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// mustStat stats p, failing the test on error.
func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// TestGetPutHitMiss: Put then Get returns byte-identical content; a file on
// disk that was never Put is a miss (Get never reads the file itself); a
// path that cannot be stat'd (nil fi) is a miss.
func TestGetPutHitMiss(t *testing.T) {
	c := New()
	p := writeFile(t, t.TempDir(), "a.go", "package a\n\nfunc A() {}\n")
	b := []byte("package a\n\nfunc A() {}\n")
	c.Put(p, b, mustStat(t, p))

	got, ok := c.Get(p, mustStat(t, p))
	if !ok {
		t.Fatal("expected hit after Put")
	}
	if !bytes.Equal(got, b) {
		t.Fatalf("Get returned different bytes: got %q want %q", got, b)
	}
	// On-disk file that was never Put: miss, and the file is not read.
	unread := writeFile(t, t.TempDir(), "b.go", "never cached")
	if _, ok := c.Get(unread, mustStat(t, unread)); ok {
		t.Fatal("never-Put path must be a miss")
	}
	// Path that cannot be stat'd (nil fi): miss.
	if _, ok := c.Get(filepath.Join(t.TempDir(), "missing.go"), nil); ok {
		t.Fatal("nil-stat must be a miss")
	}
}

// TestGetPutWithStat: the caller's observed stat pins the freshness tag.
func TestGetPutWithStat(t *testing.T) {
	c := New()
	p := writeFile(t, t.TempDir(), "a.go", "one\n")
	c.Put(p, []byte("one\n"), mustStat(t, p))
	got, ok := c.Get(p, mustStat(t, p))
	if !ok || string(got) != "one\n" {
		t.Fatalf("expected hit with caller stat, got %q ok=%v", got, ok)
	}
}

// TestCanonicalKeys: two textual spellings of the same file (one with a
// redundant "/." segment) share one cache entry after canonicalization.
func TestCanonicalKeys(t *testing.T) {
	c := New()
	dir := t.TempDir()
	abs := writeFile(t, dir, "a.go", "same bytes\n")
	spelled := abs + string(filepath.Separator) + "." // Clean collapses to abs
	if spelled == abs {
		t.Fatal("spelling must differ textually to prove canonicalization")
	}
	c.Put(abs, []byte("same bytes\n"), mustStat(t, abs))
	got, ok := c.Get(spelled, mustStat(t, abs))
	if !ok {
		t.Fatal("non-canonical spelling must hit the canonical entry")
	}
	if string(got) != "same bytes\n" {
		t.Fatalf("canonical-key Get returned wrong bytes: %q", got)
	}
}

// TestFreshnessRotationSize: editing a file to a different size invalidates
// the entry — the next Get is a miss and the new bytes are served.
func TestFreshnessRotationSize(t *testing.T) {
	c := New()
	dir := t.TempDir()
	p := writeFile(t, dir, "a.go", "one\n")
	c.Put(p, []byte("one\n"), mustStat(t, p))
	if _, ok := c.Get(p, mustStat(t, p)); !ok {
		t.Fatal("expected hit on unchanged file")
	}
	// Different size => different stat => miss.
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(p, mustStat(t, p)); ok {
		t.Fatal("expected miss after size change")
	}
	// Re-Put the new bytes: served fresh.
	c.Put(p, []byte("one\ntwo\nthree\n"), mustStat(t, p))
	got, ok := c.Get(p, mustStat(t, p))
	if !ok {
		t.Fatal("expected hit after re-Put")
	}
	if string(got) != "one\ntwo\nthree\n" {
		t.Fatalf("stale bytes served: %q", got)
	}
}

// TestFreshnessRotationMtime: a same-size edit (mtime-only change) is also a
// miss, so a touched file never serves stale bytes.
func TestFreshnessRotationMtime(t *testing.T) {
	c := New()
	dir := t.TempDir()
	p := writeFile(t, dir, "a.go", "same-size\n")
	c.Put(p, []byte("same-size\n"), mustStat(t, p))
	if _, ok := c.Get(p, mustStat(t, p)); !ok {
		t.Fatal("expected hit on unchanged file")
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get(p, mustStat(t, p)); ok {
		t.Fatal("expected miss after mtime-only change (same size)")
	}
}

// TestLRUEviction: with the default 128-entry cap, inserting one past the
// cap evicts the least-recently-used entry; a Get promotes an entry to MRU
// so it survives eviction.
func TestLRUEviction(t *testing.T) {
	c := New()
	dir := t.TempDir()
	keys := make([]string, LRUCap)
	for i := range keys {
		keys[i] = writeFile(t, dir, fmt.Sprintf("f%03d.go", i), "x")
		c.Put(keys[i], []byte("x"), mustStat(t, keys[i]))
	}
	if _, ok := c.Get(keys[0], mustStat(t, keys[0])); !ok {
		t.Fatal("expected first entry to be present at cap")
	}
	// keys[0] is now MRU; inserting one more evicts the previous LRU
	// (keys[1]).
	c.Put(writeFile(t, dir, "overflow.go", "x"), []byte("x"), mustStat(t, filepath.Join(dir, "overflow.go")))
	if _, ok := c.Get(keys[1], mustStat(t, keys[1])); ok {
		t.Fatal("expected LRU entry evicted past the cap")
	}
	if _, ok := c.Get(keys[0], mustStat(t, keys[0])); !ok {
		t.Fatal("expected promoted entry to survive eviction")
	}
	if len(c.order) > LRUCap {
		t.Fatalf("order grew past cap: %d", len(c.order))
	}
}

// TestEntrySizeCap: a file at the per-entry bound is cached; one byte over
// is skipped entirely, and a skipped Put never disturbs existing entries.
func TestEntrySizeCap(t *testing.T) {
	c := New()
	dir := t.TempDir()
	atBound := bytes.Repeat([]byte("a"), MaxEntryBytes)
	p := writeFile(t, dir, "at.go", string(atBound))
	c.Put(p, atBound, mustStat(t, p))
	if _, ok := c.Get(p, mustStat(t, p)); !ok {
		t.Fatal("entry exactly at the bound should be cached")
	}
	over := bytes.Repeat([]byte("b"), MaxEntryBytes+1)
	p2 := writeFile(t, dir, "over.go", string(over))
	c.Put(p2, over, mustStat(t, p2))
	if _, ok := c.Get(p2, mustStat(t, p2)); ok {
		t.Fatal("entry over the bound must not be cached")
	}
	if _, ok := c.Get(p, mustStat(t, p)); !ok {
		t.Fatal("at-bound entry disturbed by a skipped Put")
	}
}

// TestInvalidate: Invalidate drops the entry so the next Get is a miss, and
// is a no-op for unknown paths.
func TestInvalidate(t *testing.T) {
	c := New()
	p := writeFile(t, t.TempDir(), "a.go", "x")
	c.Put(p, []byte("x"), mustStat(t, p))
	if _, ok := c.Get(p, mustStat(t, p)); !ok {
		t.Fatal("expected hit before Invalidate")
	}
	c.Invalidate(p)
	if _, ok := c.Get(p, mustStat(t, p)); ok {
		t.Fatal("expected miss after Invalidate")
	}
	c.Invalidate(filepath.Join(t.TempDir(), "never.go")) // must not panic
}

// TestConcurrentGetPut: parallel Put/Get on distinct paths and on one shared
// path. Every Get must return exactly one writer's full content — never a
// mixture — and a Put must always be immediately visible (run with -race).
func TestConcurrentGetPut(t *testing.T) {
	c := New()
	dir := t.TempDir()
	const n = 8
	paths := make([]string, n)
	contents := make([][]byte, n)
	stats := make([]os.FileInfo, n)
	for g := 0; g < n; g++ {
		contents[g] = bytes.Repeat([]byte{byte('a' + g)}, 100)
		paths[g] = writeFile(t, dir, fmt.Sprintf("g%d.go", g), string(contents[g]))
		stats[g] = mustStat(t, paths[g])
	}
	shared := writeFile(t, dir, "shared.go", "")
	sharedStat := mustStat(t, shared)
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				c.Put(paths[g], contents[g], stats[g])
				got, ok := c.Get(paths[g], stats[g])
				if !ok {
					t.Error("expected hit after Put")
					return
				}
				if !bytes.Equal(got, contents[g]) {
					t.Errorf("got corrupted bytes for goroutine %d", g)
					return
				}
			}
		}(g)
	}
	// Shared-path contention: the last Put wins; a Get must return exactly
	// one goroutine's content, never a mixture.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			c.Put(shared, contents[i%n], sharedStat)
			got, ok := c.Get(shared, sharedStat)
			if !ok {
				t.Error("expected hit on shared path")
				return
			}
			matched := false
			for _, want := range contents {
				if bytes.Equal(got, want) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("shared-path Get returned mixed bytes: %q", got)
				return
			}
		}
	}()
	wg.Wait()
}
