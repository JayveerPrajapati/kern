package project

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// watchRecorder collects onChange batches from a Watch started with a short
// debounce window (see startShortWatch).
type watchRecorder struct {
	mu      sync.Mutex
	batches [][]string
	ch      chan []string
}

// startShortWatch starts Watch with watchDebounce shortened to 40ms (80ms on
// the native path, which doubles it) and a long poll interval so the poll
// ticker does not interfere with the debounce assertions. It returns a
// recorder fed by onChange.
func startShortWatch(t *testing.T, dir string) *watchRecorder {
	t.Helper()
	old := watchDebounce
	watchDebounce = 40 * time.Millisecond
	t.Cleanup(func() { watchDebounce = old })
	rec := &watchRecorder{ch: make(chan []string, 64)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Watch(ctx, dir, 2*time.Second, func(changes []index.Change, ix *index.Index) {
		var files []string
		for _, c := range changes {
			files = append(files, string(c.Kind)+":"+c.File)
		}
		rec.mu.Lock()
		rec.batches = append(rec.batches, files)
		rec.mu.Unlock()
		select {
		case rec.ch <- files:
		default:
		}
	}, nil)
	return rec
}

func batchHasFile(files []string, name string) bool {
	for _, f := range files {
		if strings.HasSuffix(f, ":"+name) {
			return true
		}
	}
	return false
}

func watcherAvailable(t *testing.T) bool {
	t.Helper()
	if NativeWatcherSupported() {
		return true
	}
	if _, _, err := WatcherCommand(t.TempDir()); err == nil {
		return true
	}
	return false
}

// TestDebounceSendTrailing pins the trailing semantics of debounceSend: the
// timer resets on every notify, so send fires exactly once per burst, after
// the LAST notify settles (not after the first), carrying the last path.
func TestDebounceSendTrailing(t *testing.T) {
	const settle = 80 * time.Millisecond
	var (
		mu      sync.Mutex
		calls   int
		gotPath string
		firedAt time.Time
	)
	send := func(p string) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotPath = p
		firedAt = time.Now()
	}
	notify, stop := debounceSend(send, settle)
	defer stop()

	notify("foo.go")
	time.Sleep(30 * time.Millisecond)
	notify("foo_test.go")
	time.Sleep(30 * time.Millisecond)
	lastNotify := time.Now()
	notify("foo_test.go")

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("debounceSend never fired")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// No second fire may arrive after the first settles.
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("debounceSend fired %d times, want exactly 1", calls)
	}
	if firedAt.Before(lastNotify) {
		t.Fatalf("send fired at %v, before the last notify at %v", firedAt, lastNotify)
	}
	if d := firedAt.Sub(lastNotify); d < 50*time.Millisecond {
		t.Fatalf("send fired %v after the last notify; expected it to wait for the burst to settle", d)
	}
	if gotPath != "foo_test.go" {
		t.Fatalf("send got path %q, want the last notified path %q", gotPath, "foo_test.go")
	}
}

// TestWatchTrailingMergesRelatedBurst: editing foo.go and foo_test.go
// back-to-back rebuilds ONCE, with both files in the change set.
func TestWatchTrailingMergesRelatedBurst(t *testing.T) {
	if !watcherAvailable(t) {
		t.Skip("no native or external file-event watcher available; debounce not exercised")
	}
	dir := t.TempDir()
	rec := startShortWatch(t, dir)
	// Let the baseline rebuild and the native watcher registration settle.
	time.Sleep(150 * time.Millisecond)

	writeFile(t, dir, "foo.go", "package main\n\nfunc Foo() {}\n")
	time.Sleep(40 * time.Millisecond)
	writeFile(t, dir, "foo_test.go", "package main\n\nfunc TestFoo(t *testing.T) {}\n")

	select {
	case batch := <-rec.ch:
		if len(batch) != 2 {
			t.Fatalf("expected one merged batch with both files, got %v", batch)
		}
		if !batchHasFile(batch, "foo.go") {
			t.Errorf("merged batch missing foo.go: %v", batch)
		}
		if !batchHasFile(batch, "foo_test.go") {
			t.Errorf("merged batch missing foo_test.go: %v", batch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the merged change batch")
	}

	// No further rebuild may fire within this window.
	time.Sleep(400 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if n := len(rec.batches); n != 1 {
		t.Fatalf("expected exactly one onChange batch, got %d: %v", n, rec.batches)
	}
}

// TestWatchSeparatesUnrelatedBursts: edits separated by well past the
// debounce window rebuild separately — twice in total.
func TestWatchSeparatesUnrelatedBursts(t *testing.T) {
	if !watcherAvailable(t) {
		t.Skip("no native or external file-event watcher available; debounce not exercised")
	}
	dir := t.TempDir()
	rec := startShortWatch(t, dir)
	time.Sleep(150 * time.Millisecond)

	writeFile(t, dir, "a.go", "package main\n\nfunc A() {}\n")
	select {
	case <-rec.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the first change batch")
	}
	// Wait well past the trailing window before the second edit.
	time.Sleep(300 * time.Millisecond)

	writeFile(t, dir, "b.go", "package main\n\nfunc B() {}\n")
	select {
	case <-rec.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the second change batch")
	}

	time.Sleep(400 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if n := len(rec.batches); n != 2 {
		t.Fatalf("expected exactly two onChange batches, got %d: %v", n, rec.batches)
	}
}
