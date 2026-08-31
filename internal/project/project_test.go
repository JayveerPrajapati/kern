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
	fw := newFileWatcher(t.TempDir(), func() {})
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
	fw := newFileWatcher(t.TempDir(), func() {})
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

	deadline := time.Now().Add(2 * time.Second)
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
