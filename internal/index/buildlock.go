package index

import (
	"errors"
	"log"
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// buildLockScope is the workspace lock scope that serializes index rebuilds
// across processes: <root>/.kern/locks/index-build.lock (M3). The kern-mcp
// watcher holds the separate "index-watch" scope; the two mechanisms never
// collide, so a watcher rebuild and a CLI rebuild can overlap safely — both
// are content-addressed and write through the SQLite-primary store (WAL).
const buildLockScope = "index-build"

// BuildLockWait is the total wall-clock budget for waiting on another live
// process's in-flight rebuild before falling back to rebuilding directly.
// Exported (a var, not a const) so tests can shrink it.
var BuildLockWait = 60 * time.Second

// buildLockPoll is the retry cadence while a live process holds the lock.
var buildLockPoll = 250 * time.Millisecond

// tryBuildLock acquires the exclusive cross-process build lock for root
// without blocking. It returns a release func and true when acquired. When
// another LIVE process holds the lock it returns (nil, false) — the holder's
// liveness is the flock/exclusive-create primitive itself, so a dead holder
// never wedges the lock. Any other failure (e.g. the .kern/locks directory
// cannot be created) is treated as "no lock": callers fall back to rebuilding
// directly, never hang.
func tryBuildLock(root string) (release func(), acquired bool) {
	l, err := lock.Acquire(root, buildLockScope)
	if err != nil {
		if !errors.Is(err, lock.ErrLocked) {
			// Lock weirdness (unwritable .kern, exotic fs): rebuild without
			// the lock — conservative, never hang.
			log.Printf("kern index: build lock unavailable for %s (%v); rebuilding without it", root, err)
		}
		return nil, false
	}
	return func() { _ = l.Release() }, true
}

// debouncedRebuild runs a stale-index rebuild under the exclusive
// cross-process build lock so concurrent kern processes that detect the same
// staleness share ONE rebuild instead of each paying the full cost (M3). It
// is the debounce layer under LoadOrBuild, EnsureFresh and BuildPersisted.
//
// check re-loads the persisted index from disk and reports whether it is
// fresh — the same freshness decision that put the caller on the rebuild
// path. rebuild performs the actual update/build cascade and returns the
// rebuilt payload (the index, or an ensure-fresh result).
//
// Conservative by construction:
//   - the lock is only taken on the rebuild path; the fast fresh-load path
//     stays lock-free;
//   - when the lock is free we re-check freshness UNDER the lock before
//     rebuilding, so a rebuild completed by another process while we were
//     deciding is reused, never redone;
//   - when another live process holds the lock we poll for up to
//     BuildLockWait, re-checking freshness after each acquisition — a
//     completed rebuild is reused, never re-run;
//   - a holder that exceeds the budget, an unacquirable lock, or any other
//     lock weirdness falls back to rebuilding directly — we never hang
//     forever and never serve a stale index as fresh without re-checking.
func debouncedRebuild[T any](root string, check func() (*T, bool), rebuild func() (*T, error)) (*T, error) {
	release, acquired := tryBuildLock(root)
	if acquired {
		defer release()
		if ix, fresh := check(); fresh {
			return ix, nil
		}
		return rebuild()
	}
	log.Printf("kern index: another process is rebuilding the index for %s; waiting (up to %s)", root, BuildLockWait)
	deadline := time.Now().Add(BuildLockWait)
	for time.Now().Before(deadline) {
		time.Sleep(buildLockPoll)
		release, acquired = tryBuildLock(root)
		if acquired {
			defer release()
			if ix, fresh := check(); fresh {
				log.Printf("kern index: reused index for %s rebuilt by another process", root)
				return ix, nil
			}
			return rebuild()
		}
	}
	// Bounded wait exhausted: the holder is either very slow or wedged. Fall
	// back to rebuilding directly (never hang); the rebuild itself is
	// concurrency-safe (SQLite WAL with the JSON fallback under contention).
	log.Printf("kern index: build lock for %s held longer than %s; rebuilding without it", root, BuildLockWait)
	return rebuild()
}
