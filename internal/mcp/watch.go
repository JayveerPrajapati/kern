package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// Background index watch: the MCP server keeps workspace-root indexes warm
// between tool calls by polling for staleness on a tick and rebuilding stale
// indexes through the exact same on-demand path tool calls use
// (project.Session.Index), so a source edit never leaves the next tool call
// blocking on a cold rebuild. The watch is silent: nothing is written to
// stdout (the stdio MCP protocol lives there), and stderr only sees a single
// line when a rebuild actually fails.
//
// Configuration:
//   - KERN_MCP_WATCH=0 disables the watch entirely (default: enabled).
//   - KERN_MCP_WATCH_INTERVAL sets the poll interval in seconds (default 5;
//     invalid or non-positive values fall back to 5).
//
// Shutdown: the loop stops on ctx cancellation (the stdio and HTTP serve
// paths pass their signal context) or on Server.Close, which closes watchStop
// and drains an in-flight rebuild for up to watchShutdownTimeout. No rebuild
// starts after the stop signal, and the watch goroutine never leaks.
const (
	// watchDefaultInterval is the poll interval when KERN_MCP_WATCH_INTERVAL
	// is unset or invalid.
	watchDefaultInterval = 5 * time.Second
	// watchShutdownTimeout bounds how long Close waits for an in-flight
	// rebuild to drain, matching the server's existing 5s shutdown style.
	watchShutdownTimeout = 5 * time.Second
)

// watchIntervalFromEnv returns the background-watch poll interval from
// KERN_MCP_WATCH_INTERVAL (seconds, parsed with strconv; invalid or unset
// values fall back to 5s) — or 0 when KERN_MCP_WATCH=0 disables the watch.
func watchIntervalFromEnv() time.Duration {
	if os.Getenv("KERN_MCP_WATCH") == "0" {
		return 0
	}
	if v := os.Getenv("KERN_MCP_WATCH_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return watchDefaultInterval
}

// StartBackgroundWatch spawns the server's implicit background index watcher:
// every interval it rebuilds any stale workspace-root index through the same
// on-demand path tool calls use (project.Session.Index), so the index stays
// warm between tool calls. It returns the receiver for chaining.
//
// The watcher is single-flight (a tick is skipped while a rebuild is
// running), writes nothing to stdout, and reports a rebuild failure as a
// single stderr line. It stops when ctx is cancelled or when Close is called
// (Close additionally drains an in-flight rebuild for up to 5s). An interval
// <= 0 disables the watcher. The first call wins; later calls are no-ops.
// The stdio and HTTP serve paths start it automatically with the
// KERN_MCP_WATCH / KERN_MCP_WATCH_INTERVAL environment configuration.
func (s *Server) StartBackgroundWatch(ctx context.Context, interval time.Duration) *Server {
	if interval <= 0 {
		return s
	}
	s.watchStarted.Do(func() {
		go s.watchLoop(ctx, interval)
	})
	return s
}

// watchLoop is the background watch goroutine. It ticks every interval until
// ctx is cancelled or the server is closed, then exits, closing watchDone so
// Close and tests can observe the clean stop.
func (s *Server) watchLoop(ctx context.Context, interval time.Duration) {
	defer func() {
		if s.watchDone != nil {
			close(s.watchDone)
		}
	}()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.watchStop:
			return
		case <-ticker.C:
			s.maybeRebuildIndexes()
		}
	}
}

// maybeRebuildIndexes checks every workspace root's index for staleness and
// rebuilds stale ones via the exact on-demand path tool calls use
// (project.Session.Index), whose session mutex serializes rebuilds so a
// background rebuild never races a tool call's index access. Single-flight:
// if a rebuild is already running, the tick is skipped instead of stacking a
// concurrent one. A failed rebuild prints one line to stderr and is retried
// on the next tick; it never blocks tool calls.
func (s *Server) maybeRebuildIndexes() {
	s.watchMu.Lock()
	if s.watchBusy {
		s.watchMu.Unlock()
		return
	}
	s.watchBusy = true
	s.watchWG.Add(1)
	s.watchMu.Unlock()

	go func() {
		defer s.watchWG.Done()
		defer func() {
			s.watchMu.Lock()
			s.watchBusy = false
			s.watchMu.Unlock()
		}()
		for _, root := range s.workspaceRoots() {
			if isFilesystemRoot(root) {
				continue
			}
			// C5 leadership election: only one kern process may rebuild a
			// root at a time. flock is per open-file-description, so a lock
			// held by any other server (even in this process) makes us skip
			// the tick; the lock is held for the duration of the rebuild,
			// and the kernel releases it if the holder dies.
			leader, err := lock.Acquire(root, "index-watch")
			switch {
			case err == nil:
				s.rebuildRoot(root)
				_ = leader.Release()
			case errors.Is(err, lock.ErrLocked):
				// Another kern process owns the rebuild this tick. The next
				// tool call still rebuilds on demand if this process's own
				// index is stale — election only dedupes background warmers.
				continue
			default:
				// Lock infrastructure failure: fail open, rebuild anyway so
				// the index never silently starves.
				s.rebuildRoot(root)
			}
		}
	}()
}

// rebuildRoot rebuilds one workspace root's index through the exact on-demand
// path tool calls use (project.Session.Index), whose session mutex serializes
// rebuilds so a background rebuild never races a tool call's index access. A
// failed rebuild prints one line to stderr and is retried on the next tick;
// it never blocks tool calls.
func (s *Server) rebuildRoot(root string) {
	if _, err := s.sessionFor(root).Index(); err != nil {
		// One stderr line, only when a rebuild actually fails; the
		// next tick retries. Nothing ever goes to stdout.
		fmt.Fprintf(os.Stderr, "kern-mcp: background watch: rebuild %s: %v\n", root, err)
		return
	}
	// Record the build so the "first build in progress" notice is
	// not printed for a root the watcher already warmed.
	s.indexedRoots.Store(root, true)
}

// stopWatch stops the background watch: no rebuild may start after this, and
// an in-flight rebuild is drained for up to watchShutdownTimeout. Safe to
// call multiple times and on servers that never started the watch.
func (s *Server) stopWatch() {
	if s.watchStop == nil {
		return
	}
	s.watchOnce.Do(func() { close(s.watchStop) })
	drained := make(chan struct{})
	go func() {
		s.watchWG.Wait()
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(watchShutdownTimeout):
	}
}
