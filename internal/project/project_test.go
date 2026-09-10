package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
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
		Watch(ctx, dir, 50*time.Millisecond, func(changes []index.Change, ix *index.Index) {
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
	go Watch(ctx, dir, 50*time.Millisecond, func(changes []index.Change, ix *index.Index) {
		mu.Lock()
		for _, c := range changes {
			got = append(got, string(c.Kind)+":"+c.File)
		}
		mu.Unlock()
	}, nil)

	time.Sleep(300 * time.Millisecond)
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
	go Watch(ctx, dir, 20*time.Millisecond, func(changes []index.Change, ix *index.Index) {
		mu.Lock()
		changeCount++
		mu.Unlock()
		select {
		case <-done:
		default:
			close(done)
		}
	}, nil)
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
	loaded, err := index.Load(root)
	if err != nil || loaded == nil {
		t.Fatalf("persisted index after Close: %v", err)
	}
	if len(loaded.Symbols) == 0 {
		t.Fatal("persisted index empty — async save did not land")
	}
}
