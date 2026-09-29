package watcher

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// testGit runs a git command in dir with machine-global git hooks disabled
// (repo convention: every fixture git invocation prepends -c core.hooksPath=;
// see internal/index/identity_test.go testGit).
func testGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=", "-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeFile writes body into dir/name, failing the test on error.
func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// fixtureRepo creates a tiny git repo (a few indexable .go files, committed)
// with a persisted baseline index, exactly like `kern index` would produce.
// t.TempDir guarantees a clean, isolated root.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n\nfunc main() { println(hello()) }\n")
	writeFile(t, dir, "hello.go", "package main\n\n// hello returns a greeting.\nfunc hello() string { return \"hi\" }\n")
	testGit(t, dir, "init")
	testGit(t, dir, "config", "user.name", "watcher-test")
	testGit(t, dir, "config", "user.email", "watcher-test@example.com")
	testGit(t, dir, "add", ".")
	testGit(t, dir, "commit", "-qm", "baseline")
	if _, err := index.BuildPersisted(dir); err != nil {
		t.Fatalf("BuildPersisted: %v", err)
	}
	return dir
}

// waitFor polls cond until it holds or the timeout elapses, failing the test.
func waitFor(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

func TestIntervalMsFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"unset disabled", "", 0},
		{"zero disabled", "0", 0},
		{"negative disabled", "-5", 0},
		{"invalid disabled", "abc", 0},
		{"whitespace disabled", "   ", 0},
		{"positive enables", "100", 100 * time.Millisecond},
		{"larger interval", "2500", 2500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvIntervalMs, tt.env)
			if got := IntervalMsFromEnv(); got != tt.want {
				t.Errorf("IntervalMsFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestWatcherReloadsOnChange covers the core contract: a source change makes
// the index stale, the watcher rebuilds into a NEW index and fires OnReload
// with it, and — crucially — the fresh index reflects the change and no
// rebuild loop follows (the next wake proves the rebuilt index fresh).
func TestWatcherReloadsOnChange(t *testing.T) {
	dir := fixtureRepo(t)
	reloaded := make(chan *index.Index, 4)
	w := New(Options{
		Root:     dir,
		Interval: 50 * time.Millisecond,
		OnReload: func(ix *index.Index) error {
			reloaded <- ix
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	// Change the tree: add a file and edit another.
	writeFile(t, dir, "extra.go", "package main\n\n// extra is a new symbol.\nfunc extra() string { return \"x\" }\n")
	writeFile(t, dir, "hello.go", "package main\n\n// hello returns a greeting.\nfunc hello() string { return \"hi there\" }\n")

	var fresh *index.Index
	select {
	case fresh = <-reloaded:
	case <-time.After(15 * time.Second):
		t.Fatal("reload did not fire after a source change")
	}
	if fresh == nil {
		t.Fatal("OnReload received a nil index")
	}
	// The fresh index must reflect the change: extra.go is indexed now.
	if len(fresh.FileHashes) != 3 {
		t.Errorf("fresh index has %d files, want 3 (extra.go added)", len(fresh.FileHashes))
	}
	if _, ok := fresh.FileHashes["extra.go"]; !ok {
		t.Errorf("fresh index missing extra.go (keys: %v)", mapKeys(fresh.FileHashes))
	}
	// No rebuild loop: a settled tree must not fire a second reload.
	select {
	case <-reloaded:
		t.Fatal("unexpected second reload on a settled tree")
	case <-time.After(300 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestWatcherSingleFlight pins that a slow rebuild is never re-entered: wakes
// that fire while the rebuild is blocked are skipped by the busy gate.
func TestWatcherSingleFlight(t *testing.T) {
	dir := fixtureRepo(t)
	// Make the tree stale before the watcher starts so the first wake enters
	// the rebuild path immediately.
	writeFile(t, dir, "extra.go", "package main\n\n// extra is a new symbol.\nfunc extra() string { return \"x\" }\n")

	started := make(chan struct{})
	release := make(chan struct{})
	var builds atomic.Int32
	slow := func(root string, prev *index.Index) (*index.Index, error) {
		if builds.Add(1) == 1 {
			close(started)
		}
		<-release // block the rebuild until the test releases it
		return index.BuildPersisted(root)
	}
	var reloads atomic.Int32
	w := New(Options{
		Root:     dir,
		Interval: 40 * time.Millisecond,
		Build:    slow,
		OnReload: func(ix *index.Index) error { reloads.Add(1); return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	select {
	case <-started:
	case <-time.After(15 * time.Second):
		t.Fatal("slow rebuild never started")
	}
	// Several wake intervals pass while the rebuild is blocked: each must be
	// skipped, so Build stays at exactly one call.
	time.Sleep(3 * w.interval)
	if got := builds.Load(); got != 1 {
		t.Fatalf("rebuild re-entered: Build called %d times, want 1", got)
	}
	close(release)
	waitFor(t, 15*time.Second, "reload after release", func() bool { return reloads.Load() == 1 })
	if got := builds.Load(); got != 1 {
		t.Fatalf("Build called %d times after release, want 1", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestWatcherStopsOnCancel pins the shutdown contract: Run returns promptly
// when ctx is cancelled and no reload fires after cancellation.
func TestWatcherStopsOnCancel(t *testing.T) {
	dir := fixtureRepo(t)
	writeFile(t, dir, "extra.go", "package main\n\n// extra is a new symbol.\nfunc extra() string { return \"x\" }\n")

	var reloads atomic.Int32
	w := New(Options{
		Root:     dir,
		Interval: 20 * time.Millisecond,
		OnReload: func(ix *index.Index) error { reloads.Add(1); return nil },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()

	waitFor(t, 15*time.Second, "first reload", func() bool { return reloads.Load() >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	// Nothing may fire after the cancel returns.
	time.Sleep(100 * time.Millisecond)
	if got := reloads.Load(); got != 1 {
		t.Fatalf("reload fired after cancel: %d reloads, want 1", got)
	}
}

// mapKeys returns the sorted keys of m for readable failure messages.
func mapKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
