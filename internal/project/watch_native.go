package project

// Native file-event watchers, zero dependencies (stdlib syscall only):
// - darwin : kqueue (EVFILT_VNODE on every directory fd)
// - linux  : inotify (IN_MODIFY/IN_CREATE/IN_DELETE/IN_MOVED on the tree)
// - others : no native source → nil → external inotifywait/fswatch or the
// polling fallback in index.Stale().
// When a native source is available it replaces the external-tool dependency
// on macOS/Linux while keeping kern's default build stdlib-only.

import (
	"context"
	"sync"
	"time"
)

// nativeSource abstracts one OS file-event source. Event() delivers a
// channel of relative paths (deduplicated per batch, but not debounced) for
// every indexable file under the watched root that changed. Close releases
// the OS resources.
type nativeSource interface {
	Event() <-chan string
	Close()
}

// nativeWatcherSupported is set per platform (kqueue/inotify = true, others
// default false) so callers can report the active watching backend without
// constructing a source.
var nativeWatcherSupported = false

// NativeWatcherSupported reports whether the current platform can use the
// built-in stdlib file watcher (kqueue on darwin, inotify on linux).
func NativeWatcherSupported() bool { return nativeWatcherSupported }

// WatchMode returns a human-readable description of the file-watching backend
// the caller can expect for root: native stdlib watcher, an external
// inotifywait/fswatch tool, or polling.
func WatchMode(root string) string {
	if NativeWatcherSupported() {
		return "native (stdlib file-events)"
	}
	if _, _, err := WatcherCommand(root); err == nil {
		return "file-event (inotifywait/fswatch)"
	}
	return "polling"
}

// newNativeFileWatcher starts a background native watcher for root, or
// returns nil when this platform has no native source (fall back to the
// external tool / polling path). notify receives the relative path of the
// last changed file in each collapsed burst.
func newNativeFileWatcher(root string, notify func(path string)) *fileWatcher {
	src := newNativeSource(root)
	if src == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	fw := &fileWatcher{ctx: ctx, cancel: cancel, notify: notify}
	fw.wg.Add(1)
	go func() {
		defer fw.wg.Done()
		defer src.Close()
		send, stop := debounceSend(notify, 200*time.Millisecond)
		defer stop()
		ev := src.Event()
		for {
			select {
			case <-ctx.Done():
				return
			case p, ok := <-ev:
				if !ok {
					return
				}
				send(p)
			}
		}
	}()
	return fw
}

// debounceSend returns a function that, once called, fires send() after the
// streak of events settles for `settle` (collapsing bursts like the external
// watcher does). Trailing: every notify resets the timer, so send fires
// exactly once per burst, after the LAST event settles, carrying the path of
// the most recent event. stop() aborts a pending fire.
func debounceSend(send func(path string), settle time.Duration) (notify func(path string), stop func()) {
	var (
		mu   sync.Mutex
		t    *time.Timer
		pend bool
		last string
		fire = func() {
			mu.Lock()
			defer mu.Unlock()
			if pend {
				pend = false
				send(last)
			}
		}
	)
	notify = func(path string) {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
		}
		last = path
		t = time.AfterFunc(settle, fire)
		pend = true
	}
	stop = func() {
		mu.Lock()
		defer mu.Unlock()
		if t != nil {
			t.Stop()
			t = nil
		}
		pend = false
	}
	return notify, stop
}
