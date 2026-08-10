package project

import (
	"context"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Watch monitors root for source-file changes and calls onChange after every
// change batch with the freshly rebuilt index. It uses the native OS file-event
// watcher (inotifywait on Linux, fswatch on macOS) when available, which gives
// near-real-time notification with a short debounce; otherwise it falls back to
// polling every pollInterval. The callback runs on a background goroutine and
// must not block for long. Returns ctx.Err() when the context is cancelled.
func Watch(ctx context.Context, root string, pollInterval time.Duration, onChange func(changes []index.Change, ix *index.Index)) error {
	ch := make(chan struct{}, 1)
	notify := func() {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	fw := newFileWatcher(root, notify)
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

	var (
		timer   *time.Timer
		timerMu sync.Mutex
	)
	var rebuild func()
	rebuild = func() {
		prev := map[string]string{}
		if ix, err := index.Load(root); err == nil && ix != nil {
			prev = ix.FileHashes
		}
		cur := index.FileHashes(root)
		changes := index.Diff(prev, cur)
		if len(changes) == 0 {
			return
		}
		ix, err := index.Build(root)
		if err != nil {
			return
		}
		_ = ix.Save()
		if onChange != nil {
			onChange(changes, ix)
		}
	}
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

	if native {
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ch:
				schedule()
			}
		}
	}

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
