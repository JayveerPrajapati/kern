package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/topology"
)

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls cond until it returns true or the timeout elapses. Watch
// runs on a background goroutine, so tests wait on observed state rather
// than sleeping a fixed interval.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestSessionResolvesEmptyRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	s := New("", "s1")
	if s.Root != cwd {
		t.Fatalf("expected cwd, got %q", s.Root)
	}
	if s.Session != "s1" {
		t.Fatalf("expected session kept, got %q", s.Session)
	}
}

func TestSessionIndexBuildAndStaleRebuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\n// Greet says hello.\nfunc Greet() {}\n")
	s := New(root, "")
	// Close drains the B6 background index saves (and stops the watcher) so
	// the async JSON write cannot race TempDir cleanup at test end.
	defer s.Close()

	ix, err := s.Index()
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols in first build")
	}
	first := ix

	// Fresh cache must be reused (same pointer).
	again, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatal("expected cached index reused while fresh")
	}

	// Adding a source file makes the cached index stale; Index must rebuild.
	writeFile(t, root, "extra.go", "package main\nfunc Extra() {}\n")
	s.Invalidate()
	rebuilt, err := s.Index()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sym := range rebuilt.Symbols {
		if sym.Name == "Extra" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected rebuilt index to include Extra")
	}
}

// TestSessionArchitectureInvalidatedOnRebuild pins the cache contract
// graph.Arch relies on: the derived architecture cache is cleared both by
// Invalidate() and by Index() when a rebuild publishes a fresh index
// instance, so a session never serves a stale architecture for the new
// index. The cached value is recomputed on demand and reflects the new tree.
func TestSessionArchitectureInvalidatedOnRebuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() { local() }\nfunc local() {}\n")
	s := New(root, "")
	defer s.Close() // stop watcher + drain B6 background saves before TempDir cleanup

	ix1, err := s.Index()
	if err != nil {
		t.Fatalf("Index 1: %v", err)
	}
	first := topology.RenderArch(s.Architecture(ix1))
	if s.arch == nil {
		t.Fatal("Architecture must populate the cache")
	}
	// Cache hit: the second call returns the identical cached value.
	if got := topology.RenderArch(s.Architecture(ix1)); got != first {
		t.Fatal("cached Architecture changed between calls")
	}

	// A rebuild with a different community structure must drop the cache.
	writeFile(t, root, "extra.go", "package main\n\nfunc Alpha() { beta() }\nfunc beta() {}\n")
	s.Invalidate()
	ix2, err := s.Index()
	if err != nil {
		t.Fatalf("Index 2: %v", err)
	}
	if ix2 == ix1 {
		t.Fatal("precondition failed: rebuild must produce a new index instance")
	}
	if s.arch != nil {
		t.Fatal("rebuild must invalidate the cached architecture")
	}
	second := topology.RenderArch(s.Architecture(ix2))
	if second == first {
		t.Fatal("recomputed architecture after rebuild should reflect the new community structure")
	}
}

func TestSessionRecordBestEffort(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := New(t.TempDir(), "rec-test")
	// Must not panic even though no optimization happened.
	s.Record("run_build", "test", "", 100, 40)
	if s.Recorder() == nil {
		t.Fatal("expected recorder with writable cache")
	}
}

func TestLookupWatcherCmd(t *testing.T) {
	name, args := lookupWatcherCmd(t.TempDir())
	if name == "" {
		// No native watcher tool available in test env — that's fine.
		t.Skip("no inotifywait/fswatch available; skipping watcher test")
	}
	if len(args) == 0 {
		t.Fatal("expected non-empty args for watcher command")
	}
}

func TestFileWatcherFallbackWhenNoTool(t *testing.T) {
	// If no native watcher is installed, newFileWatcher must return nil
	// gracefully (not panic).
	fw := newFileWatcher(t.TempDir(), func(string) {})
	if fw == nil {
		// Expected on systems without inotifywait/fswatch.
		return
	}
	fw.Stop()
}

func TestFileWatcherStopsGracefully(t *testing.T) {
	if _, err := exec.LookPath("inotifywait"); err != nil && runtime.GOOS == "linux" {
		if _, err := exec.LookPath("fswatch"); err != nil {
			t.Skip("no file-event tool available")
		}
	}
	if _, err := exec.LookPath("fswatch"); err != nil && runtime.GOOS == "darwin" {
		t.Skip("no fswatch available")
	}
	fw := newFileWatcher(t.TempDir(), func(string) {})
	if fw == nil {
		t.Skip("file watcher not available on this platform")
	}
	fw.Stop()
	fw.Stop() // double close should be safe
}

func TestWatchPollFallback(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nfunc hello() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	go func() {
		_ = Watch(ctx, dir, 50*time.Millisecond, func(changes []index.Change, ix *index.Index) {
			mu.Lock()
			for _, c := range changes {
				got = append(got, string(c.Kind)+":"+c.File)
			}
			mu.Unlock()
			select {
			case <-done:
			default:
				close(done)
			}
		}, nil)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for first change event")
	}
	// A change batch must have been observed (initial build reports the file
	// as added).
	mu.Lock()
	defer mu.Unlock()
	if len(got) == 0 {
		t.Fatal("expected at least one change event")
	}
}

func TestWatchDetectsModification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	var mu sync.Mutex
	var got []string
	go func() {
		_ = Watch(ctx, dir, 50*time.Millisecond, func(changes []index.Change, ix *index.Index) {
			mu.Lock()
			for _, c := range changes {
				got = append(got, string(c.Kind)+":"+c.File)
			}
			mu.Unlock()
		}, nil)
	}()

	// Watch runs a synchronous baseline build on start that fires onChange
	// with the initial tree; wait for it before modifying so the edit diffs
	// against the start state and is reported as "modified" (not folded
	// into the baseline). A fixed sleep would both slow the suite and flake
	// when the baseline build lags under load.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) > 0
	})
	if err := os.WriteFile(path, []byte("package main\n\nfunc hello() {}\nfunc bye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Generous deadline: fs-event delivery can lag well past 2s under
	// sustained system load (observed flaking in CI-style full-suite runs).
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		found := false
		for _, c := range got {
			if c == "modified:main.go" {
				found = true
			}
		}
		mu.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected a modified event, got %v", got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWatchRebuildsAreSerialized(t *testing.T) {
	// ix-11: concurrent rebuild triggers must be serialized so prev map
	// access has no data race. The race detector (go test -race) is the
	// primary check here — it flags any unsynchronized read/write of prev.
	dir := t.TempDir()
	src := "package main\n\nfunc hello() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var mu sync.Mutex
	var changeCount int
	var done = make(chan struct{})
	go func() {
		_ = Watch(ctx, dir, 20*time.Millisecond, func(changes []index.Change, ix *index.Index) {
			mu.Lock()
			changeCount++
			mu.Unlock()
			select {
			case <-done:
			default:
				close(done)
			}
		}, nil)
	}()
	<-done
}

// TestSessionNewKeepsRootVerbatim pins the Session facade's root contract:
// New stores Root exactly as given (trailing slashes, relative paths and `..`
// components are NOT normalized here). Path normalization (abs/clean) is the
// job of mcp.resolveRoot, which runs before it calls project.New. SENTINEL:
// if New ever starts normalizing, these assertions fail, flagging the
// behavior change — callers (the MCP server) rely on the verbatim root for
// workspace-confinement comparisons.
func TestSessionNewKeepsRootVerbatim(t *testing.T) {
	trailing := t.TempDir() + string(filepath.Separator)
	cases := []struct {
		name string
		root string
	}{
		{"trailing-slash", trailing},
		{"relative", filepath.Join(".", "some-rel-dir")},
		{"dotdot", filepath.Join("..", "some-parent-dir")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.root, "")
			defer s.Close()
			if s.Root != tc.root {
				t.Fatalf("New(%q): Root = %q, want verbatim %q", tc.root, s.Root, tc.root)
			}
		})
	}
}

// TestSessionCloseIdempotent covers Close (0% before): it must release the
// watcher without panicking and tolerate being called twice (Close guards
// nil watchers and watcher.Stop is a sync.Once).
func TestSessionCloseIdempotent(t *testing.T) {
	s := New(t.TempDir(), "closer")
	s.Close()
	s.Close() // double close must be safe
	// A session whose watcher is nil (no fswatch/inotifywait on PATH) must
	// also Close cleanly.
	New(t.TempDir(), "nil-watcher").Close()
}

// TestSessionStaleWhileRevalidateServesStale is the B1 regression: while a
// rebuild is in flight, a concurrent Index() call must be served the stale
// cached snapshot IMMEDIATELY instead of blocking for the rebuild (the
// original held one mutex across the whole rebuild, so the first tool call
// after an edit blocked every other tool call).
func TestSessionStaleWhileRevalidateServesStale(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\nfunc Greet() {}\n")
	s := New(root, "")
	defer s.Close() // stop watcher + drain B6 background saves before TempDir cleanup
	first, err := s.Index()
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	// Simulate an in-flight rebuild (deterministic: no timing dependence on
	// how long a real build takes).
	s.mu.Lock()
	s.rebuilding = true
	s.mu.Unlock()

	served := make(chan *index.Index, 1)
	go func() {
		ix, err := s.Index()
		if err != nil {
			t.Errorf("concurrent Index: %v", err)
		}
		served <- ix
	}()

	select {
	case ix := <-served:
		if ix != first {
			t.Error("stale-while-revalidate must serve the cached snapshot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Index() blocked behind an in-flight rebuild — stale-while-revalidate is broken")
	}

	// Release the simulated rebuild so the session stays usable.
	s.mu.Lock()
	s.rebuilding = false
	s.cond.Broadcast()
	s.mu.Unlock()
}

// TestSessionFirstBuildWaiters covers the wait path: callers that arrive
// before any index exists wait for the in-flight first build instead of
// starting a second one (single-flight), then receive its result.
func TestSessionFirstBuildWaiters(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	s := New(root, "")
	defer s.Close() // stop watcher + drain B6 background saves before TempDir cleanup

	// Simulate a first build in flight with nothing served yet.
	s.mu.Lock()
	s.rebuilding = true
	s.mu.Unlock()

	type result struct {
		ix  *index.Index
		err error
	}
	res := make(chan result, 1)
	go func() {
		ix, err := s.Index()
		res <- result{ix, err}
	}()

	select {
	case <-res:
		t.Fatal("waiter must block until the first build finishes")
	case <-time.After(100 * time.Millisecond):
	}

	// Complete the simulated build.
	s.mu.Lock()
	built := &index.Index{Root: root}
	s.ix = built
	s.buildResult = built
	s.rebuilding = false
	s.buildErr = nil
	s.cond.Broadcast()
	s.mu.Unlock()

	select {
	case r := <-res:
		if r.err != nil || r.ix == nil {
			t.Fatalf("waiter got (%v, %v); want the built index", r.ix, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter was not woken by the completed build")
	}
}

// TestSessionConcurrentIndexInvalidateStress runs concurrent Index() and
// Invalidate() calls to shake out lock/rebuild races (run with -race).
func TestSessionConcurrentIndexInvalidateStress(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\nfunc Greet() {}\n")
	s := New(root, "")
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				ix, err := s.Index()
				if err != nil {
					t.Errorf("Index: %v", err)
					return
				}
				if len(ix.Symbols) == 0 {
					t.Error("empty index served")
					return
				}
				if n%4 == 0 {
					s.Invalidate()
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestFreshnessCooldownAdaptsToWatcher pins B5: with a file-event watcher
// active the staleness re-check window relaxes to 5s (events bypass it via
// Invalidate); without one it stays at the tight 1s polling window.
func TestFreshnessCooldownAdaptsToWatcher(t *testing.T) {
	s := New(t.TempDir(), "")
	s.watcher = &fileWatcher{} // event-driven
	if got := s.freshnessCooldown(); got != staleCooldownNative {
		t.Errorf("with watcher: freshnessCooldown = %v, want %v", got, staleCooldownNative)
	}
	s.watcher = nil // polling fallback
	if got := s.freshnessCooldown(); got != staleCooldown {
		t.Errorf("without watcher: freshnessCooldown = %v, want %v", got, staleCooldown)
	}
}

// TestSessionAsyncSavePersistsBeforeClose pins B6: Index() returns before the
// background save necessarily lands, but Close() drains in-flight saves — so
// after Close, the persisted store must contain the rebuilt index.
func TestSessionAsyncSavePersistsBeforeClose(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\n// Greet says hello.\nfunc Greet() {}\n")
	s := New(root, "")
	defer s.Close() // stop watcher + drain B6 background saves before TempDir cleanup
	ix, err := s.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(ix.Symbols) == 0 {
		t.Fatal("expected symbols in first build")
	}
	s.Close() // must drain the async save
	// Load through the store the session actually persists to: under the
	// sqlite tag the session writes the SQLite store only (the JSON cache
	// is the fallback for builds without the tag), so index.Load (JSON)
	// would report nothing there. LoadSQLite returns (nil, nil) when the
	// store does not exist yet.
	var loaded *index.Index
	var lerr error
	if index.SQLiteEnabled() {
		loaded, lerr = index.LoadSQLite(root)
	} else {
		loaded, lerr = index.Load(root)
	}
	if lerr != nil || loaded == nil {
		t.Fatalf("persisted index after Close: %v", lerr)
	}
	if len(loaded.Symbols) == 0 {
		t.Fatal("persisted index empty — async save did not land")
	}
}

// hasSymbol reports whether ix contains a symbol with the given name.
func hasSymbol(ix *index.Index, name string) bool {
	for _, s := range ix.Symbols {
		if s.Name == name {
			return true
		}
	}
	return false
}

// hashesMatchTree asserts that ix.FileHashes equals the current FileHashes of
// the tree — the observable freshness contract both the incremental Update
// path and the full-Build fallback must satisfy.
func hashesMatchTree(t *testing.T, ix *index.Index, root string) {
	t.Helper()
	cur, err := index.FileHashes(root)
	if err != nil {
		t.Fatalf("FileHashes: %v", err)
	}
	if len(ix.FileHashes) != len(cur) {
		t.Errorf("index has %d files, tree has %d", len(ix.FileHashes), len(cur))
	}
	for f, h := range cur {
		if ix.FileHashes[f] != h {
			t.Errorf("FileHashes[%s] = %q, want %q", f, ix.FileHashes[f], h)
		}
	}
}

// TestSessionRebuildSmallDiffUsesIncrementalUpdate: with a persisted previous
// index and ONE file changed, Session.Index() reconciles via the incremental
// index.Update path. The diff path is observable: unchanged files are reused
// (ReusedResults >= 1), which a full Build would never report, and the
// reconciled index reflects the change and matches the tree.
func TestSessionRebuildSmallDiffUsesIncrementalUpdate(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "a.go", "package demo\n\nfunc A() {}\n")
	writeFile(t, root, "b.go", "package demo\n\nfunc B() {}\n")
	// Persist a previous index deterministically (synchronous Save) so
	// rebuildIndex loads it as the incremental-Update base from the store.
	prev, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := prev.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Modify ONE file: add function B2.
	writeFile(t, root, "b.go", "package demo\n\nfunc B() {}\n\nfunc B2() {}\n")
	s := New(root, "")
	defer s.Close() // stop watcher + drain B6 background saves before TempDir cleanup
	ix, err := s.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if !hasSymbol(ix, "B2") {
		t.Fatal("small-diff reconcile missing newly added symbol B2")
	}
	// The incremental Update path was used: the unchanged a.go (and go.mod)
	// were reused from the persisted prev. A full Build would reuse nothing.
	if got := ix.ReusedResults(); got < 1 {
		t.Errorf("ReusedResults = %d, want >= 1 (small-diff reconcile must reuse unchanged files)", got)
	}
	// Correctness: the reconciled index matches the current tree.
	hashesMatchTree(t, ix, root)
}

// TestSessionRebuildLargeDiffBacksOffToFullBuild: when the change set exceeds
// index.CatchUpMaxChanges, rebuildIndex skips the incremental Update (which
// would re-parse nearly every file) and falls back to a full Build — the
// returned index must still be correct and fresh.
func TestSessionRebuildLargeDiffBacksOffToFullBuild(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const n = index.CatchUpMaxChanges + 10 // 210 files: every one modified below
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		writeFile(t, root, filepath.Join("pkg", fmt.Sprintf("f%03d.go", i)),
			fmt.Sprintf("package pkg\n\nfunc F%d() {}\n", i))
	}
	// Persist a previous index deterministically (synchronous Save).
	prev, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := prev.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Modify EVERY file (add one symbol each) so the change set is n > 200.
	for i := 0; i < n; i++ {
		writeFile(t, root, filepath.Join("pkg", fmt.Sprintf("f%03d.go", i)),
			fmt.Sprintf("package pkg\n\nfunc F%d() {}\n\nfunc G%d() {}\n", i, i))
	}
	// Prove the scenario is above the back-off threshold: the persisted prev
	// loads (so rebuildIndex had an Update base) and the tree diff exceeds it.
	loaded, err := index.Load(root)
	if err != nil || loaded == nil {
		t.Fatalf("expected persisted prev to load, got (%v, %v)", loaded, err)
	}
	cur, err := index.FileHashes(root)
	if err != nil {
		t.Fatalf("FileHashes: %v", err)
	}
	if changes := len(index.Diff(loaded.FileHashes, cur)); changes <= index.CatchUpMaxChanges {
		t.Fatalf("fixture diff = %d, want > %d (back-off must actually trigger)", changes, index.CatchUpMaxChanges)
	}
	s := New(root, "")
	defer s.Close() // stop watcher + drain B6 background saves before TempDir cleanup
	ix, err := s.Index()
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	// Correctness: the back-off must not break freshness — the new symbols
	// from every modified file are present and the index matches the tree.
	if !hasSymbol(ix, "G0") || !hasSymbol(ix, fmt.Sprintf("G%d", n-1)) {
		t.Error("large-diff back-off result missing newly added symbols")
	}
	hashesMatchTree(t, ix, root)
	// Consistent with the full-Build fallback: a clean Build reuses nothing.
	if got := ix.ReusedResults(); got != 0 {
		t.Errorf("ReusedResults = %d, want 0 on the large-diff back-off (full Build reuses nothing)", got)
	}
}

func TestSessionExternalStoreHotReload(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\nfunc Initial() {}\n")

	s := New(root, "")
	defer s.Close()

	ix1, err := s.Index()
	if err != nil {
		t.Fatalf("Index 1: %v", err)
	}
	if !hasSymbol(ix1, "Initial") {
		t.Fatal("expected Initial symbol in ix1")
	}

	// External modification: new file added and built externally
	writeFile(t, root, "extra.go", "package main\n\nfunc ExternalAdded() {}\n")
	ixExt, err := index.Build(root)
	if err != nil {
		t.Fatalf("Build external: %v", err)
	}
	// Give a slight delay to ensure distinct filesystem timestamp if needed
	time.Sleep(10 * time.Millisecond)
	if err := ixExt.Save(); err != nil {
		t.Fatalf("Save external: %v", err)
	}

	// Session.Index() must immediately hot-reload the newly saved store
	ix2, err := s.Index()
	if err != nil {
		t.Fatalf("Index 2: %v", err)
	}
	if !hasSymbol(ix2, "ExternalAdded") {
		t.Fatal("expected ExternalAdded symbol in hot-reloaded index")
	}
}

// stopWatcher stops and detaches the session's file-event watcher so a
// timing-sensitive test is hermetic: a native watcher (fswatch/inotifywait)
// could fire Invalidate() mid-test and race the probe bookkeeping.
func stopWatcher(s *Session) {
	if s.watcher != nil {
		s.watcher.Stop()
		s.watcher = nil
	}
}

// TestSessionProbeBurstSingleFlight (D1): a burst of ProbeFresh calls that
// all arrive after the freshness cooldown expired must run the staleness
// probe ONCE, not once per call. The first caller in the window becomes the
// probe runner; the rest wait on the session cond and reuse the memoized
// verdict (staleUntil on fresh). The probeStale seam counts + blocks so the
// burst is deterministic: the runner is mid-probe when the burst lands.
func TestSessionProbeBurstSingleFlight(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\nfunc Greet() {}\n")
	s := New(root, "")
	defer s.Close()
	stopWatcher(s)

	if _, err := s.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	// Expire the cooldown so the burst lands AFTER it: a fresh staleUntil
	// would short-circuit every caller with zero probes (nothing to prove).
	s.mu.Lock()
	s.staleUntil = time.Time{}
	s.mu.Unlock()

	var (
		mu      sync.Mutex
		probes  int
		entered = make(chan struct{})
		release = make(chan struct{})
		once    sync.Once
	)
	orig := probeStale
	probeStale = func(ix *index.Index) bool {
		mu.Lock()
		probes++
		mu.Unlock()
		once.Do(func() { close(entered) }) // the runner is now mid-probe
		<-release                          // hold the probe open until the burst has arrived
		return false                       // fresh: the memo must re-serve this verdict
	}
	t.Cleanup(func() { probeStale = orig })

	// Runner: probes once, blocks inside the probe.
	runnerDone := make(chan bool, 1)
	go func() { runnerDone <- s.ProbeFresh() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("runner never entered the probe")
	}

	// Burst: every call lands while the runner is mid-probe; all must block
	// on the single-flight cond instead of running the probe themselves.
	const burst = 8
	results := make(chan bool, burst)
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.ProbeFresh()
		}()
	}
	// Give the burst time to reach the cond wait before releasing the probe.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if probes != 1 {
		t.Fatalf("probe count during the burst = %d, want 1 (single-flight memo)", probes)
	}
	mu.Unlock()

	close(release)
	if got := <-runnerDone; !got {
		t.Fatal("runner verdict: want fresh=true")
	}
	wg.Wait()
	close(results)
	for r := range results {
		if !r {
			t.Fatal("burst caller got stale=false; the memo must re-serve the fresh verdict")
		}
	}
	// Still exactly one probe for the whole window.
	mu.Lock()
	defer mu.Unlock()
	if probes != 1 {
		t.Fatalf("total probe count = %d, want 1", probes)
	}
	// The memo is the same staleUntil the pre-change code set: it expires
	// when the cooldown does, never later.
	s.mu.Lock()
	until := s.staleUntil
	s.mu.Unlock()
	if d := time.Until(until); d <= 0 || d > s.freshnessCooldown() {
		t.Fatalf("memo staleUntil = %v (%v out), want a fresh cooldown window", until, d)
	}
}

// TestSessionProbeMemoExpiresWithCooldown (b): the memoized fresh verdict IS
// the pre-existing cooldown (staleUntil) — calls within the window re-serve
// it with no probe, and once it passes the probe re-runs. The memo can never
// extend staleness beyond the pre-existing cooldown.
func TestSessionProbeMemoExpiresWithCooldown(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\nfunc Greet() {}\n")
	s := New(root, "")
	defer s.Close()
	stopWatcher(s)

	if _, err := s.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	// Expire the cooldown so the first ProbeFresh actually probes.
	s.mu.Lock()
	s.staleUntil = time.Time{}
	s.mu.Unlock()

	var (
		mu     sync.Mutex
		probes int
	)
	orig := probeStale
	probeStale = func(ix *index.Index) bool {
		mu.Lock()
		probes++
		mu.Unlock()
		return false // fresh
	}
	t.Cleanup(func() { probeStale = orig })

	// Window start: the probe runs once and memoizes fresh.
	if !s.ProbeFresh() {
		t.Fatal("first probe: want fresh")
	}
	mu.Lock()
	if probes != 1 {
		t.Fatalf("probes after first call = %d, want 1", probes)
	}
	mu.Unlock()

	// Within the window: memoized, no re-probe.
	if !s.ProbeFresh() {
		t.Fatal("memoized call: want fresh")
	}
	mu.Lock()
	if probes != 1 {
		t.Fatalf("memo must not re-probe within the cooldown (probes = %d)", probes)
	}
	mu.Unlock()

	// Expire the cooldown exactly the way time would: the memo expires with
	// it and the next call re-probes.
	s.mu.Lock()
	s.staleUntil = time.Time{}
	s.mu.Unlock()
	if !s.ProbeFresh() {
		t.Fatal("post-expiry probe: want fresh")
	}
	mu.Lock()
	defer mu.Unlock()
	if probes != 2 {
		t.Fatalf("expired memo must re-probe (probes = %d, want 2)", probes)
	}
}

// TestSessionProbeStaleVerdictInvalidates: a stale probe verdict is
// memoized as invalidation (mirroring Invalidate) — the session reports no
// cached index afterwards, so the D1 identity is "noindex" and lookups
// miss; the next Index() rebuilds and serves a fresh index.
func TestSessionProbeStaleVerdictInvalidates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "app.go", "package main\n\nfunc Greet() {}\n")
	s := New(root, "")
	defer s.Close()
	stopWatcher(s)

	if _, err := s.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	s.mu.Lock()
	s.staleUntil = time.Time{}
	s.mu.Unlock()

	var probes int
	orig := probeStale
	probeStale = func(ix *index.Index) bool {
		probes++
		return true // stale
	}
	t.Cleanup(func() { probeStale = orig })

	if s.ProbeFresh() {
		t.Fatal("stale verdict must report not fresh")
	}
	if probes != 1 {
		t.Fatalf("probes = %d, want 1", probes)
	}
	if _, ok := s.CachedIndex(); ok {
		t.Fatal("CachedIndex must report no index after a stale probe")
	}
	// The stale memo stays authoritative without re-probing: a follow-up
	// call in the same window returns the documented vacuous true (no cached
	// identity is left to distrust) WITHOUT running the probe again — and
	// CachedIndex keeps reporting nothing, so the D1 identity stays "noindex".
	if !s.ProbeFresh() {
		t.Fatal("post-invalidation ProbeFresh: documented vacuous true")
	}
	if probes != 1 {
		t.Fatalf("stale memo must not re-probe (probes = %d)", probes)
	}
	if _, ok := s.CachedIndex(); ok {
		t.Fatal("CachedIndex must stay empty after the stale memo")
	}
	// The next Index() rebuilds and serves a fresh index.
	ix, err := s.Index()
	if err != nil {
		t.Fatalf("Index after stale probe: %v", err)
	}
	if ix == nil || len(ix.Symbols) == 0 {
		t.Fatal("Index() must rebuild after a stale probe")
	}
}

// TestSessionProbeFreshDetectsEditAfterCooldown (d): the memo never blinds
// the probe — once the cooldown expires, a REAL staleness probe detects an
// edit and invalidates, so no cache entry keyed to the old identity can be
// served beyond the pre-existing cooldown window.
func TestSessionProbeFreshDetectsEditAfterCooldown(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module demo\n\ngo 1.22\n")
	writeFile(t, root, "a.go", "package main\n\nfunc A() string { return \"v1\" }\n")
	s := New(root, "")
	defer s.Close()
	stopWatcher(s)

	if _, err := s.Index(); err != nil {
		t.Fatalf("Index: %v", err)
	}
	// Unchanged tree: fresh (served from the cooldown the build recorded).
	if !s.ProbeFresh() {
		t.Fatal("unchanged tree must be fresh")
	}

	// Edit + expire the cooldown: the next probe must detect the change.
	writeFile(t, root, "a.go", "package main\n\nfunc A() string { return \"v2 edited\" }\n")
	s.mu.Lock()
	s.staleUntil = time.Time{}
	s.mu.Unlock()

	if s.ProbeFresh() {
		t.Fatal("edited tree must probe stale — the memo must not blind the probe")
	}
	if _, ok := s.CachedIndex(); ok {
		t.Fatal("stale probe must invalidate the cached index")
	}
	// Rebuild reconciles with the edited tree (freshness contract).
	ix, err := s.Index()
	if err != nil {
		t.Fatalf("Index after edit: %v", err)
	}
	hashesMatchTree(t, ix, root)
}
