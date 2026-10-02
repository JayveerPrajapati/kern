package index

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// restoreBuildLockTimings restores the (test-shrunk) debounce timings after
// each test that overrides them.
func restoreBuildLockTimings(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		BuildLockWait = 60 * time.Second
		buildLockPoll = 250 * time.Millisecond
	})
}

// TestDebouncedRebuildRebuildsWhenStillStale pins the uncontended path: the
// lock is free, the re-check under the lock says stale, so exactly one
// rebuild runs.
func TestDebouncedRebuildRebuildsWhenStillStale(t *testing.T) {
	restoreBuildLockTimings(t)
	root := t.TempDir()
	buildLockPoll = 10 * time.Millisecond

	checkCalls := 0
	rebuildCalls := 0
	check := func() (*Index, bool) {
		checkCalls++
		return nil, false // still stale
	}
	rebuild := func(prev *Index) (*Index, error) {
		rebuildCalls++
		return &Index{Root: root}, nil
	}
	ix, err := debouncedRebuild(root, check, rebuild)
	if err != nil {
		t.Fatalf("debouncedRebuild: %v", err)
	}
	if ix == nil || ix.Root != root {
		t.Fatalf("expected rebuilt index, got %+v", ix)
	}
	if rebuildCalls != 1 {
		t.Fatalf("rebuild ran %d times, want exactly 1", rebuildCalls)
	}
	if checkCalls != 1 {
		t.Fatalf("re-check ran %d times, want 1 (once under the lock)", checkCalls)
	}
}

// TestDebouncedRebuildReusesAfterHolderFinishes pins the debounce: while
// another LIVE process holds the build lock (simulated by holding it in this
// test), debouncedRebuild must WAIT for the holder, re-check freshness, and
// REUSE the holder's result — never rebuild.
func TestDebouncedRebuildReusesAfterHolderFinishes(t *testing.T) {
	restoreBuildLockTimings(t)
	root := t.TempDir()
	BuildLockWait = 10 * time.Second
	buildLockPoll = 20 * time.Millisecond

	// Simulate another live process mid-rebuild: hold the exclusive build
	// lock, then release it 300ms later (the "holder's rebuild finishes").
	holder, err := lock.Acquire(root, buildLockScope)
	if err != nil {
		t.Fatalf("acquire holder lock: %v", err)
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = holder.Release()
	}()

	rebuildCalls := 0
	checkCalls := 0
	check := func() (*Index, bool) {
		checkCalls++
		// Fresh as soon as we can acquire: the holder's rebuild landed.
		return &Index{Root: root}, true
	}
	rebuild := func(prev *Index) (*Index, error) {
		rebuildCalls++
		return nil, os.ErrInvalid // must never run
	}
	start := time.Now()
	ix, err := debouncedRebuild(root, check, rebuild)
	if err != nil {
		t.Fatalf("debouncedRebuild: %v", err)
	}
	elapsed := time.Since(start)
	if ix == nil || ix.Root != root {
		t.Fatalf("expected the reused index, got %+v", ix)
	}
	if rebuildCalls != 0 {
		t.Fatalf("rebuild ran %d times, want 0 (must reuse the holder's result)", rebuildCalls)
	}
	if elapsed < 250*time.Millisecond {
		t.Fatalf("did not wait for the holder to finish: waited %s", elapsed)
	}
	if checkCalls < 1 {
		t.Fatalf("expected a freshness re-check after acquiring the lock, got %d", checkCalls)
	}
}

// TestDebouncedRebuildFallsBackAfterTimeout pins the never-hang guarantee: a
// holder that exceeds BuildLockWait must not wedge the caller — the rebuild
// falls back to running directly.
func TestDebouncedRebuildFallsBackAfterTimeout(t *testing.T) {
	restoreBuildLockTimings(t)
	root := t.TempDir()
	BuildLockWait = 300 * time.Millisecond
	buildLockPoll = 20 * time.Millisecond

	holder, err := lock.Acquire(root, buildLockScope)
	if err != nil {
		t.Fatalf("acquire holder lock: %v", err)
	}
	defer holder.Release()

	rebuildCalls := 0
	check := func() (*Index, bool) {
		return nil, false // never fresh while the holder is wedged
	}
	rebuild := func(prev *Index) (*Index, error) {
		rebuildCalls++
		return &Index{Root: root}, nil
	}
	start := time.Now()
	ix, err := debouncedRebuild(root, check, rebuild)
	if err != nil {
		t.Fatalf("debouncedRebuild: %v", err)
	}
	elapsed := time.Since(start)
	if ix == nil || ix.Root != root {
		t.Fatalf("expected the fallback-rebuilt index, got %+v", ix)
	}
	if rebuildCalls != 1 {
		t.Fatalf("fallback rebuild ran %d times, want exactly 1", rebuildCalls)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("bounded wait exceeded: %s (must never hang)", elapsed)
	}
}

// TestLoadOrBuildConcurrentDebounce runs two goroutines through the real
// LoadOrBuild path against the same stale index: both must converge on a
// valid fresh index, and the second must reuse the first's rebuild rather
// than produce a corrupt or divergent result.
func TestLoadOrBuildConcurrentDebounce(t *testing.T) {
	restoreBuildLockTimings(t)
	root := t.TempDir()
	buildLockPoll = 10 * time.Millisecond

	src := filepath.Join(root, "sample.go")
	if err := os.WriteFile(src, []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrBuild(root); err != nil {
		t.Fatalf("warm LoadOrBuild: %v", err)
	}
	// Make the persisted index stale (add a symbol) and remove it entirely
	// so both goroutines take the full rebuild path.
	if err := os.WriteFile(src, []byte("package sample\n\nfunc Hello() string { return \"hi\" }\n\nfunc NewSymbol() int { return 42 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(StorePath(root))
	_ = os.Remove(SQLitePath(root))

	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]*Index, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = LoadOrBuild(root)
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent LoadOrBuild %d: %v", i, err)
		}
		if results[i] == nil {
			t.Fatalf("concurrent LoadOrBuild %d returned nil index", i)
		}
		found := false
		for _, s := range results[i].Symbols {
			if s.Name == "NewSymbol" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("concurrent LoadOrBuild %d served an index without the new symbol (stale served as fresh)", i)
		}
	}
	// The persisted index must be fresh for the next caller.
	if ix, err := Load(root); err != nil || ix == nil {
		t.Fatalf("persisted index unloadable after concurrent rebuild: %v", err)
	} else if ix.Stale() {
		t.Fatal("persisted index still stale after concurrent rebuild")
	}
	// The build lock file must exist (the mechanism left its marker).
	if _, err := os.Stat(filepath.Join(root, ".kern", "locks", buildLockScope+".lock")); err != nil {
		t.Fatalf("build lock file missing: %v", err)
	}
}
