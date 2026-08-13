package project

// Native file-event watchers, zero dependencies (stdlib syscall only):
//
//	- darwin : kqueue (EVFILT_VNODE on every directory fd)
//	- linux  : inotify (IN_MODIFY/IN_CREATE/IN_DELETE/IN_MOVED on the tree)
//	- others : no native source → nil → external inotifywait/fswatch or the
//	           polling fallback in index.Stale().
//
// When a native source is available it replaces the external-tool dependency
// on macOS/Linux while keeping kern's default build stdlib-only.

import (
	"context"
	"sync"
	"time"
)

// nativeSource abstracts one OS file-event source. Event() delivers a
// channel that fires (deduplicated, but not debounced) whenever an indexable
// file under the watched root changes. Close releases the OS resources.
type nativeSource interface {
	Event() <-chan struct{}
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
// external tool / polling path).
func newNativeFileWatcher(root string, notify func()) *fileWatcher {
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
			case _, ok := <-ev:
				if !ok {
					return
				}
				send()
			}
		}
	}()
	return fw
}

// debounceSend returns a function that, once called, fires send() after the
// streaks of events settle for `settle` (collapsing bursts like the external
// watcher does). stop() aborts a pending fire.
func debounceSend(send func(), settle time.Duration) (notify func(), stop func()) {
	var (
		mu   sync.Mutex
		t    *time.Timer
		pend bool
		fire = func() {
			mu.Lock()
			defer mu.Unlock()
			if pend {
				pend = false
				send()
			}
		}
	)
	notify = func() {
		mu.Lock()
		defer mu.Unlock()
		if t == nil {
			t = time.AfterFunc(settle, fire)
		}
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
