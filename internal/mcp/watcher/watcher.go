// Package watcher implements ADR-0011's reactive file watcher: a lightweight
// polling loop that keeps the MCP server's symbol index fresh without a
// native file-event dependency. Every interval it cheaply proves the watched
// root's index is still current (index.FreshnessProof's git tree-OID fast
// path — two git queries on a clean tree, a content walk only when git cannot
// vouch for the tree) and — only when the proof says stale — rebuilds into a
// NEW index instance (the EnsureFresh rebuild path: incremental index.Update
// over the previous index, falling back to a full persisted build) and hands
// it to the OnReload callback, which owns the atomic swap into the serving
// path. The live index is never mutated: handlers keep serving the previous
// instance until the callback publishes the fresh one.
//
// The watcher is opt-in and OFF by default: the server starts it only when
// KERN_MCP_WATCH_INTERVAL_MS is a positive integer (the poll interval in
// milliseconds); unset, zero, or invalid values disable it.
//
// The watcher is single-flight: a wake that fires while a rebuild is still
// running is skipped instead of stacking a concurrent rebuild. Rebuilds can
// take 30-90s on large repos, so polling stays cheap while fresh — the fresh
// path is one inline proof with no goroutine spawn. It stops when its Run
// context is cancelled, draining an in-flight rebuild first. Failures print
// exactly one stderr line (server logging style) and are retried on the next
// wake.
package watcher

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// EnvIntervalMs names the opt-in env knob: a positive integer enables the
// watcher with that poll interval in milliseconds.
const EnvIntervalMs = "KERN_MCP_WATCH_INTERVAL_MS"

// IntervalMsFromEnv parses KERN_MCP_WATCH_INTERVAL_MS. A positive integer
// yields that poll interval; unset, "0", or an invalid value yields 0
// (disabled — the default).
func IntervalMsFromEnv() time.Duration {
	v := os.Getenv(EnvIntervalMs)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}

// Options configures a Watcher. Root and Interval are required; OnReload is
// optional (a nil callback still keeps the watcher's own baseline fresh, so
// the rebuild cost is paid without a publish).
type Options struct {
	// Root is the workspace root whose index is watched (absolute preferred).
	Root string
	// Interval is the poll interval.
	Interval time.Duration
	// OnReload receives each freshly built index after a stale-triggered
	// rebuild. It owns the atomic swap into the serving path; the previous
	// index stays live until this callback publishes the new one. A non-nil
	// error logs one stderr line and the fresh index is still adopted as the
	// watcher's baseline.
	OnReload func(fresh *index.Index) error
	// Build is the rebuild primitive, exposed for tests: it must return a NEW
	// index reflecting root's current tree and persist it. Defaults to
	// rebuildFresh (EnsureFresh-style: incremental Update over prev, falling
	// back to a full persisted build).
	Build func(root string, prev *index.Index) (*index.Index, error)
}

// Watcher is a polling index-freshness watcher for one root. Create it with
// New and run it with Run; Run must not be called concurrently with itself.
type Watcher struct {
	root     string
	interval time.Duration
	onReload func(*index.Index) error
	build    func(string, *index.Index) (*index.Index, error)

	// current is the last index the watcher observed (loaded from disk or
	// freshly rebuilt): the baseline each wake proves against.
	current atomic.Pointer[index.Index]
	// busy is the single-flight gate: a wake is skipped while a rebuild runs.
	busy atomic.Bool
	// wg tracks in-flight rebuilds so Run can drain one on cancellation.
	wg sync.WaitGroup
}

// New returns a Watcher for opts. A nil Build installs the default
// EnsureFresh-style rebuild; a nil OnReload is allowed.
func New(opts Options) *Watcher {
	w := &Watcher{
		root:     opts.Root,
		interval: opts.Interval,
		onReload: opts.OnReload,
		build:    opts.Build,
	}
	if w.root == "" {
		if cwd, err := os.Getwd(); err == nil {
			w.root = cwd
		}
	}
	if abs, err := filepath.Abs(w.root); err == nil {
		w.root = abs
	}
	if w.build == nil {
		w.build = rebuildFresh
	}
	return w
}

// Run polls root's index freshness every interval until ctx is cancelled. A
// stale index is rebuilt and published via OnReload; wakes that fire while a
// rebuild is still running are skipped (single-flight). On cancellation Run
// drains an in-flight rebuild before returning. A non-positive interval keeps
// the watcher inert (nothing is polled) until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	if w.interval <= 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.wg.Wait()
			return
		case <-ticker.C:
			w.wake()
		}
	}
}

// wake runs one poll cycle. The single-flight gate is checked FIRST — before
// the (potentially slow) staleness proof — so a wake that fires while a
// rebuild is running is skipped outright instead of stacking a concurrent
// rebuild (a proof that straddles a rebuild's completion would otherwise see
// the gate clear and re-enter the rebuild with the stale baseline). The gate
// stays held by the rebuild goroutine until the publish completes.
func (w *Watcher) wake() {
	if w.busy.Swap(true) {
		return // a rebuild is still running — skip this wake
	}
	cur := w.current.Load()
	if cur == nil {
		// No in-memory baseline yet: adopt the persisted index. The server's
		// on-demand path owns the very first build, so a missing index is not
		// an error — just nothing to prove yet.
		ix, err := index.Load(w.root)
		if err != nil || ix == nil {
			w.busy.Store(false)
			return
		}
		w.current.Store(ix)
		cur = ix
	}
	// Cheap staleness proof (git tree-OID fast path; the content walk only
	// runs when git cannot vouch for the tree). Fresh → nothing to do.
	if !cur.FreshnessProof(w.root).Stale() {
		w.busy.Store(false)
		return
	}
	// Stale: rebuild off the loop so the poll keeps ticking; the busy gate
	// stays held until the rebuild + publish finish (single-flight).
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		defer w.busy.Store(false)
		w.rebuild(cur)
	}()
}

// rebuild builds a fresh index into a NEW instance — the previous one is
// never mutated — and publishes it. A failed rebuild logs ONE line and is
// retried on the next wake.
func (w *Watcher) rebuild(cur *index.Index) {
	fresh, err := w.build(w.root, cur)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kern-mcp: watcher: rebuild %s: %v\n", w.root, err)
		return
	}
	w.current.Store(fresh)
	if w.onReload == nil {
		return
	}
	if err := w.onReload(fresh); err != nil {
		fmt.Fprintf(os.Stderr, "kern-mcp: watcher: publish %s: %v\n", w.root, err)
	}
}

// rebuildFresh is the default rebuild primitive — EnsureFresh-style: the
// incremental Update over the previous index (reusing symbols and edges of
// unchanged files), falling back to a full persisted build when Update cannot
// run, then persisted exactly like `kern index` so other processes observe
// the rebuild.
func rebuildFresh(root string, prev *index.Index) (*index.Index, error) {
	if prev != nil {
		if uix, uerr := index.Update(root, prev); uerr == nil && uix != nil {
			if serr := uix.Save(); serr != nil {
				return nil, serr
			}
			return uix, nil
		}
	}
	return index.BuildPersisted(root)
}
