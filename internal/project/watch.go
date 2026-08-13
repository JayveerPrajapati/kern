package project

import (
	"context"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Watch monitors root for source-file changes and calls onChange after every
// change batch with the freshly rebuilt index. It prefers the stdlib-native
// kqueue/inotify watcher (zero dependencies), then the external OS file-event
// tool (inotifywait on Linux, fswatch on macOS), and finally falls back to
// polling every pollInterval. The callback runs on a background goroutine and
// must not block for long. onError receives every scan, build, or save failure
// instead of the watcher silently dropping it. Returns ctx.Err() when the
// context is cancelled.
func Watch(ctx context.Context, root string, pollInterval time.Duration, onChange func(changes []index.Change, ix *index.Index), onError func(err error)) error {
	ch := make(chan struct{}, 1)
	notify := func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	// Prefer the stdlib-native watcher (zero deps) for the long-lived watch
	// process; it cannot be used per-MCP-session because a kqueue/inotify fd
	// per directory would exhaust descriptors when many servers run at once.
	var fw *fileWatcher
	if NativeWatcherSupported() {
		fw = newNativeFileWatcher(root, notify)
	}
	if fw == nil {
		fw = newFileWatcher(root, notify)
	}
	native := fw != nil
	if native {
		defer fw.Stop()
	}

	debounce := 150 * time.Millisecond
	if native {
		// The file-event watcher already debounces internally; a short settle
		// window prevents a burst of edits from triggering several rebuilds.
		debounce = 300 * time.Millisecond
	}

	// Baseline hash map: seed from the persisted index when present, otherwise
	// snapshot the tree at watch start. Keeping prev in memory (instead of
	// re-reading the saved index on every rebuild) means an edit that lands
	// before the first poll or event is still diffed against the start state
	// and reported as "modified", not swallowed into an "added" event.
	prev := map[string]string{}
	if ix, err := index.Load(root); err == nil && ix != nil {
		prev = ix.FileHashes
	}

	var (
		timer     *time.Timer
		timerMu   sync.Mutex
		rebuildMu sync.Mutex
	)
	var rebuild func()
	rebuild = func() {
		rebuildMu.Lock()
		defer rebuildMu.Unlock()
		cur, err := index.FileHashes(root)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		changes := index.Diff(prev, cur)
		if len(changes) == 0 {
			return
		}
		ix, err := index.Build(root)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		if err := ix.Save(); err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		prev = cur
		if onChange != nil {
			onChange(changes, ix)
		}
	}
	// Establish the baseline synchronously at watch start: on a fresh root the
	// initial population event reports every file as "added"; on an existing
	// index it reports changes since the last save. After this the baseline is
	// fixed, so any edit arriving later is reported as "modified".
	rebuild()
	schedule := func() {
		timerMu.Lock()
		if timer == nil {
			timer = time.AfterFunc(debounce, func() {
				timerMu.Lock()
				timer = nil
				timerMu.Unlock()
				rebuild()
			})
		}
		timerMu.Unlock()
	}

	// Polling is the always-on safety net: native per-file watches (kqueue
	// NOTE_WRITE / inotify IN_MODIFY|IN_CLOSE_WRITE) catch content edits on
	// watched files, but files beyond the fd cap (kqueueMaxFds=4096) and
	// newly-created files are caught by this ticker. Keep both active
	// regardless of the native watcher so a burst of entry events is reported
	// immediately and any file the native watcher missed is still picked up
	// within pollInterval.
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
			schedule()
		case <-tick.C:
			rebuild()
		}
	}
}
