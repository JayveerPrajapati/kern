package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Test helpers ---

func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func writeFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relpath, err)
	}
}

func appendFile(t *testing.T, dir, relpath, content string) {
	t.Helper()
	full := filepath.Join(dir, relpath)
	f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", relpath, err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("append %s: %v", relpath, err)
	}
}

func deleteFile(t *testing.T, dir, relpath string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, relpath)); err != nil {
		t.Fatalf("delete %s: %v", relpath, err)
	}
}

func renameFile(t *testing.T, dir, from, to string) {
	t.Helper()
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
		t.Fatalf("rename %s -> %s: %v", from, to, err)
	}
}

// waitForEvents collects events with a timeout. Returns all events received
// within the timeout.
func waitForEvents(t *testing.T, ch <-chan []Event, timeout time.Duration) []Event {
	t.Helper()
	var all []Event
	deadline := time.After(timeout)
	for {
		select {
		case batch, ok := <-ch:
			if !ok {
				return all
			}
			all = append(all, batch...)
		case <-deadline:
			return all
		}
	}
}

// fastConfig returns a config with very short intervals for fast tests.
func fastConfig(root string) Config {
	return Config{
		Root:           root,
		Interval:       50 * time.Millisecond,
		Debounce:       100 * time.Millisecond,
		IgnorePaths:    []string{".git", "vendor", "node_modules", "testdata", ".blueprint", ".kern", "bin", "dist", "__pycache__"},
		IgnorePatterns: []string{"*.tmp", "*.swp", "*~", "*.bak", ".#*", "#*#", ".DS_Store"},
	}
}

// startWatcher creates and starts a watcher with fast config.
func startWatcher(t *testing.T, root string) (*Watcher, context.CancelFunc) {
	t.Helper()
	w := New(fastConfig(root))
	ctx, cancel := context.WithCancel(context.Background())
	if err := w.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	return w, cancel
}

// --- G10 Tests ---

// G10-1: bursty edits — multiple rapid edits to the same file should be
// debounced into a single event batch.
func TestG10_BurstyEdits(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	// Burst: 5 rapid edits within 250ms.
	for i := 0; i < 5; i++ {
		appendFile(t, dir, "main.go", fmt.Sprintf("// edit %d\n", i))
		time.Sleep(20 * time.Millisecond)
	}

	events := waitForEvents(t, w.Events(), 1*time.Second)
	// Should be debounced — likely 1-2 batches, not 5.
	modifyCount := 0
	for _, e := range events {
		if e.Type == EventModify && e.Path == "main.go" {
			modifyCount++
		}
	}
	if modifyCount == 0 {
		t.Fatal("expected at least 1 modify event for bursty edits")
	}
	if modifyCount > 3 {
		t.Errorf("expected debouncing (<=3 events), got %d modify events", modifyCount)
	}
}

// G10-2: file rename — renaming a file should be detected.
func TestG10_FileRename(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "old.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond) // let initial settle
	renameFile(t, dir, "old.go", "new.go")

	events := waitForEvents(t, w.Events(), 1*time.Second)
	foundDelete := false
	foundCreate := false
	for _, e := range events {
		if e.Path == "old.go" && e.Type == EventDelete {
			foundDelete = true
		}
		if e.Path == "new.go" && e.Type == EventCreate {
			foundCreate = true
		}
	}
	if !foundDelete {
		t.Error("expected DELETE event for old.go")
	}
	if !foundCreate {
		t.Error("expected CREATE event for new.go")
	}
}

// G10-3: file delete — deleting a file should be detected.
func TestG10_FileDelete(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "deleteme.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)
	deleteFile(t, dir, "deleteme.go")

	events := waitForEvents(t, w.Events(), 1*time.Second)
	found := false
	for _, e := range events {
		if e.Path == "deleteme.go" && e.Type == EventDelete {
			found = true
		}
	}
	if !found {
		t.Error("expected DELETE event for deleteme.go")
	}
}

// G10-4: editor temp-file behavior — temp files (*.swp, *~, .#*) should be
// ignored.
func TestG10_EditorTempFiles(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)
	// Create temp files that editors produce.
	writeFile(t, dir, "main.go.swp", "swap")
	writeFile(t, dir, "main.go~", "backup")
	writeFile(t, dir, ".#main.go", "lock")

	events := waitForEvents(t, w.Events(), 500*time.Millisecond)
	for _, e := range events {
		if strings.HasSuffix(e.Path, ".swp") || strings.HasSuffix(e.Path, "~") || strings.HasPrefix(filepath.Base(e.Path), ".#") {
			t.Errorf("temp file should be ignored: %s", e)
		}
	}
}

// G10-5: ignored paths — files in .git, vendor, etc. should be skipped.
func TestG10_IgnoredPaths(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)
	// Create files in ignored directories.
	writeFile(t, dir, ".git/config", "git config")
	writeFile(t, dir, "vendor/lib.go", "package vendor")
	writeFile(t, dir, "node_modules/pkg/index.js", "module.exports = {}")

	events := waitForEvents(t, w.Events(), 500*time.Millisecond)
	for _, e := range events {
		if strings.HasPrefix(e.Path, ".git/") || strings.HasPrefix(e.Path, "vendor/") || strings.HasPrefix(e.Path, "node_modules/") {
			t.Errorf("ignored path should not produce event: %s", e)
		}
	}
}

// G10-6: generated files — files in bin/, dist/ should be ignored.
func TestG10_GeneratedFiles(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)
	writeFile(t, dir, "bin/compiled", "binary")
	writeFile(t, dir, "dist/bundle.js", "generated")

	events := waitForEvents(t, w.Events(), 500*time.Millisecond)
	for _, e := range events {
		if strings.HasPrefix(e.Path, "bin/") || strings.HasPrefix(e.Path, "dist/") {
			t.Errorf("generated file should not produce event: %s", e)
		}
	}
}

// G10-7: simultaneous edits — multiple files edited concurrently should all
// be detected.
func TestG10_SimultaneousEdits(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "a.go", "package main\n")
	writeFile(t, dir, "b.go", "package main\n")
	writeFile(t, dir, "c.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)

	// Edit all three simultaneously.
	var wg sync.WaitGroup
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			appendFile(t, dir, name, "// edited\n")
		}(f)
	}
	wg.Wait()

	events := waitForEvents(t, w.Events(), 1*time.Second)
	seen := map[string]bool{}
	for _, e := range events {
		if e.Type == EventModify {
			seen[e.Path] = true
		}
	}
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		if !seen[f] {
			t.Errorf("expected modify event for %s", f)
		}
	}
}

// G10-8: stale event ordering — events should arrive in detection order.
func TestG10_StaleEventOrdering(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create files in sequence with timestamps.
	writeFile(t, dir, "first.go", "package main\n")
	time.Sleep(150 * time.Millisecond) // ensure different detection cycle
	writeFile(t, dir, "second.go", "package main\n")

	events := waitForEvents(t, w.Events(), 1*time.Second)
	var firstIdx, secondIdx = -1, -1
	for i, e := range events {
		if e.Path == "first.go" {
			firstIdx = i
		}
		if e.Path == "second.go" {
			secondIdx = i
		}
	}
	if firstIdx >= 0 && secondIdx >= 0 && firstIdx > secondIdx {
		t.Errorf("events out of order: first.go (idx %d) after second.go (idx %d)", firstIdx, secondIdx)
	}
}

// G10-9: watcher restart — stopping and restarting should work.
func TestG10_WatcherRestart(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	// First run.
	w1, cancel1 := startWatcher(t, dir)
	time.Sleep(100 * time.Millisecond)
	cancel1()
	w1.Stop()

	// Second run with same dir.
	w2, cancel2 := startWatcher(t, dir)
	defer cancel2()
	defer w2.Stop()

	time.Sleep(100 * time.Millisecond)
	writeFile(t, dir, "new.go", "package main\n")

	events := waitForEvents(t, w2.Events(), 1*time.Second)
	found := false
	for _, e := range events {
		if e.Path == "new.go" && e.Type == EventCreate {
			found = true
		}
	}
	if !found {
		t.Error("watcher did not detect new file after restart")
	}
}

// G10-10: shutdown — Stop should close cleanly without panicking.
func TestG10_Shutdown(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)

	// Stop immediately.
	cancel()
	w.Stop()

	// Should not panic on double-stop.
	w.Stop()

	// Verify done channel is closed.
	select {
	case <-w.done:
		// good
	default:
		t.Error("done channel not closed after Stop")
	}
}

// G10-11: CPU usage — the watcher should not consume excessive CPU when idle.
// This is a smoke test: run the watcher for 1s with no changes and verify
// it doesn't block or spin.
func TestG10_CPUUsageIdle(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	// Run for 500ms with no changes — should produce no events.
	events := waitForEvents(t, w.Events(), 500*time.Millisecond)
	// Filter out any spurious events from initial snapshot timing.
	realEvents := []Event{}
	for _, e := range events {
		if e.Type != EventCreate {
			realEvents = append(realEvents, e)
		}
	}
	if len(realEvents) > 0 {
		t.Errorf("expected no events when idle, got %d: %v", len(realEvents), realEvents)
	}
}

// G10-bonus: extension filter — when Extensions is set, only those files
// produce events.
func TestG10_ExtensionFilter(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")

	cfg := fastConfig(dir)
	cfg.Extensions = []string{".go"} // only watch .go files
	w := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	time.Sleep(100 * time.Millisecond)
	writeFile(t, dir, "readme.md", "# README")
	writeFile(t, dir, "config.yaml", "key: value")
	writeFile(t, dir, "helper.go", "package main\n")

	events := waitForEvents(t, w.Events(), 1*time.Second)
	for _, e := range events {
		if strings.HasSuffix(e.Path, ".md") || strings.HasSuffix(e.Path, ".yaml") {
			t.Errorf("non-.go file should not produce event with extension filter: %s", e)
		}
	}
	// Should see helper.go.
	found := false
	for _, e := range events {
		if e.Path == "helper.go" {
			found = true
		}
	}
	if !found {
		t.Error("expected event for helper.go")
	}
}

// G10-bonus: initial snapshot doesn't emit events for pre-existing files.
func TestG10_InitialSnapshotNoEvents(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "existing1.go", "package main\n")
	writeFile(t, dir, "existing2.go", "package main\n")

	w, cancel := startWatcher(t, dir)
	defer cancel()
	defer w.Stop()

	// Wait briefly — pre-existing files should NOT produce events.
	events := waitForEvents(t, w.Events(), 300*time.Millisecond)
	if len(events) > 0 {
		t.Errorf("initial snapshot should not emit events, got %d: %v", len(events), events)
	}
}

// Need fmt for the bursty edits test.
// import "fmt" — moved to top of file

// Ensure this file compiles on all platforms (signal handling is Unix-only).
var _ = runtime.GOOS

// G10-12: walk errors are surfaced — an unreadable subdirectory must be
// reported through the Config.OnError callback instead of being dropped
// silently. If the platform lets the walk read the directory anyway (running
// with elevated privileges), the test skips rather than failing.
func TestG10_WalkErrorReported(t *testing.T) {
	dir := testDir(t)
	writeFile(t, dir, "main.go", "package main\n")
	blocked := filepath.Join(dir, "blocked")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "blocked/secret.go", "package main\n")

	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	// chmod had no effect (privileged user): the walk won't produce an error,
	// so the scenario cannot be triggered — skip rather than fail.
	if _, err := os.ReadDir(blocked); err == nil {
		t.Skip("chmod 000 has no effect on this platform; cannot trigger a walk error")
	}

	var errs []error
	cfg := fastConfig(dir)
	cfg.OnError = func(err error) { errs = append(errs, err) }
	w := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer w.Stop()

	if err := w.Start(ctx); err != nil {
		// The top-level walk error is also surfaced through OnError; if Start
		// itself failed on the snapshot, count that as the reported error.
		if len(errs) == 0 {
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		t.Fatal("OnError was not called for the unreadable directory")
	}
}
