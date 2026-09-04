// Package watcher implements a post-write advisory feedback daemon.
// It is NOT a pre-write firewall — it observes changed files after they
// are written, debounces bursty edits, and triggers asynchronous
// Blueprint validation.
//
// Uses adaptive polling (comparing file mtimes) rather than OS-level
// events. Polling runs fast (500ms) while files change and backs off
// to 5s after quiet periods. Native OS events (kqueue/inotify) are a
// future enhancement.
package watcher

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// EventType classifies a filesystem change.
type EventType int

const (
	EventCreate EventType = iota
	EventModify
	EventDelete
	EventRename
)

// Event represents a single observed file change.
type Event struct {
	Type EventType
	Path string    // relative to watched root
	Time time.Time // when the event was detected
}

// String returns a human-readable event description.
func (e Event) String() string {
	var typ string
	switch e.Type {
	case EventCreate:
		typ = "CREATE"
	case EventModify:
		typ = "MODIFY"
	case EventDelete:
		typ = "DELETE"
	case EventRename:
		typ = "RENAME"
	}
	return fmt.Sprintf("%s %s", typ, e.Path)
}

// Config configures the watcher behavior.
type Config struct {
	Root           string        // directory to watch
	Interval       time.Duration // base (active) polling interval (default 500ms)
	MaxInterval    time.Duration // slowest polling interval when idle (default 5s)
	Debounce       time.Duration // quiet period before emitting batch (default 1s)
	IgnorePaths    []string      // paths to skip (e.g. .git, vendor)
	IgnorePatterns []string      // glob patterns to skip (e.g. *.tmp)
	Extensions     []string      // only watch these extensions (empty = all)
	// OnError receives filesystem walk errors (unreadable paths during the
	// initial snapshot walk, and the snapshot walk's top-level error). It is
	// optional: a nil callback ignores the errors, preserving the historical
	// silent behavior.
	OnError func(err error)
}

// DefaultConfig returns sensible defaults: 500ms active poll backing off to
// 5s when idle, 1s debounce, common ignore paths.
func DefaultConfig(root string) Config {
	return Config{
		Root:        root,
		Interval:    500 * time.Millisecond,
		MaxInterval: 5 * time.Second,
		Debounce:    1 * time.Second,
		IgnorePaths: []string{
			".git", "vendor", "node_modules", "testdata", ".blueprint",
			".kern", "dist", "bin", "__pycache__",
		},
		IgnorePatterns: []string{
			"*.tmp", "*.swp", "*.swo", "*~", "*.bak", "*.log",
			".#*", "#*#", ".DS_Store",
		},
	}
}

// Watcher polls a directory tree for changes and emits debounced batches of
// events. It is safe for concurrent use.
type Watcher struct {
	config  Config
	events  chan []Event
	done    chan struct{}
	running atomic.Bool          // guards Start/Stop; read by no other site
	state   map[string]fileState // current snapshot of tracked files
}

// fileState tracks a file's last-known mtime and size.
type fileState struct {
	mtime time.Time
	size  int64
}

// New creates a Watcher with the given config. Call Start to begin watching.
func New(config Config) *Watcher {
	if config.Interval == 0 {
		config.Interval = 500 * time.Millisecond
	}
	if config.MaxInterval == 0 {
		config.MaxInterval = 5 * time.Second
	}
	if config.MaxInterval < config.Interval {
		config.MaxInterval = config.Interval
	}
	if config.Debounce == 0 {
		config.Debounce = 1 * time.Second
	}
	return &Watcher{
		config: config,
		events: make(chan []Event, 16),
		done:   make(chan struct{}),
		state:  make(map[string]fileState),
	}
}

// Events returns the channel where debounced event batches are sent.
func (w *Watcher) Events() <-chan []Event { return w.events }

// Start begins polling. It takes an initial snapshot (no events emitted for
// existing files), then emits events for subsequent changes. Returns an error
// if already running or the root doesn't exist.
func (w *Watcher) Start(ctx context.Context) error {
	if !w.running.CompareAndSwap(false, true) {
		return fmt.Errorf("watcher already running")
	}

	// The root must exist and be a directory (documented Start contract:
	// "Returns an error ... if the root doesn't exist"). filepath.Walk alone
	// would silently swallow a missing root via the skip callback.
	info, err := os.Stat(w.config.Root)
	if err != nil {
		w.running.Store(false) // failed start: allow a retry
		return fmt.Errorf("initial snapshot: %w", err)
	}
	if !info.IsDir() {
		w.running.Store(false) // failed start: allow a retry
		return fmt.Errorf("initial snapshot: root is not a directory: %s", w.config.Root)
	}

	// Take initial snapshot.
	if err := w.snapshot(); err != nil {
		w.running.Store(false) // failed start: allow a retry
		return fmt.Errorf("initial snapshot: %w", err)
	}

	go w.pollLoop(ctx)
	log.Printf("watcher: adaptive polling started on %s (base %s, max %s)", w.config.Root, w.config.Interval, w.maxInterval())
	return nil
}

// Stop shuts down the watcher. Safe to call multiple times.
func (w *Watcher) Stop() {
	if !w.running.Swap(false) {
		return // already stopped
	}
	close(w.done)
}

// maxInterval returns the slowest adaptive polling interval, never below the
// active interval.
func (w *Watcher) maxInterval() time.Duration {
	if w.config.MaxInterval < w.config.Interval {
		return w.config.Interval
	}
	return w.config.MaxInterval
}

// nextPollInterval grows the poll interval toward max after a quiet poll.
// Each quiet poll doubles the current interval, capped at max and never
// dropping below base, so idle repos poll slowly and busy repos fast.
func nextPollInterval(current, base, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		next = max
	}
	if next < base {
		next = base
	}
	return next
}

// pollLoop polls the filesystem at an adaptive interval, detecting changes
// and emitting debounced batches. The interval resets to the configured base
// whenever a change is detected and doubles after each quiet poll up to
// MaxInterval, so an active repo is polled quickly and an idle repo slowly.
func (w *Watcher) pollLoop(ctx context.Context) {
	var pendingEvents []Event
	var lastChange time.Time
	interval := w.config.Interval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if len(pendingEvents) > 0 {
				select {
				case w.events <- pendingEvents:
				default:
				}
			}
			return
		case <-w.done:
			if len(pendingEvents) > 0 {
				select {
				case w.events <- pendingEvents:
				default:
				}
			}
			return
		case <-ticker.C:
			newEvents := w.detectChanges()
			if len(newEvents) > 0 {
				pendingEvents = append(pendingEvents, newEvents...)
				lastChange = time.Now()
				// Activity: poll fast again.
				if interval != w.config.Interval {
					interval = w.config.Interval
					ticker.Reset(interval)
				}
			} else {
				// Quiet poll: back off toward the max interval.
				interval = nextPollInterval(interval, w.config.Interval, w.maxInterval())
				ticker.Reset(interval)
			}
			// Debounce: emit if quiet period elapsed since last change.
			if len(pendingEvents) > 0 && time.Since(lastChange) >= w.config.Debounce {
				select {
				case w.events <- pendingEvents:
				default: // drop if consumer is slow
				}
				pendingEvents = nil
			}
		}
	}
}

// reportError surfaces a filesystem walk error through the optional OnError
// callback. A nil callback ignores the error (historical silent behavior).
func (w *Watcher) reportError(err error) {
	if w.config.OnError != nil {
		w.config.OnError(err)
	}
}

// snapshot walks the tree and records current file states. Per-visit errors
// (unreadable files/directories) are surfaced through OnError instead of
// being dropped; the top-level Walk error is both surfaced through OnError
// and returned.
func (w *Watcher) snapshot() error {
	err := filepath.Walk(w.config.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			w.reportError(err)
			return nil // skip unreadable entry, but surface the error
		}
		if info.IsDir() {
			if w.shouldIgnoreDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if w.shouldIgnoreFile(path) {
			return nil
		}
		rel, err := filepath.Rel(w.config.Root, path)
		if err != nil {
			w.reportError(err)
			return nil
		}
		rel = filepath.ToSlash(rel)
		w.state[rel] = fileState{mtime: info.ModTime(), size: info.Size()}
		return nil
	})
	if err != nil {
		w.reportError(err)
		return err
	}
	return nil
}

// detectChanges compares current filesystem state to the stored snapshot and
// returns detected events. Updates the snapshot in place.
func (w *Watcher) detectChanges() []Event {
	var events []Event
	seen := make(map[string]bool)

	filepath.Walk(w.config.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if w.shouldIgnoreDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if w.shouldIgnoreFile(path) {
			return nil
		}
		rel, err := filepath.Rel(w.config.Root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		seen[rel] = true

		prev, existed := w.state[rel]
		current := fileState{mtime: info.ModTime(), size: info.Size()}

		if !existed {
			events = append(events, Event{Type: EventCreate, Path: rel, Time: time.Now()})
		} else if prev.mtime != current.mtime || prev.size != current.size {
			events = append(events, Event{Type: EventModify, Path: rel, Time: time.Now()})
		}
		w.state[rel] = current
		return nil
	})

	// Detect deleted files (in state but not seen).
	for rel := range w.state {
		if !seen[rel] {
			events = append(events, Event{Type: EventDelete, Path: rel, Time: time.Now()})
			delete(w.state, rel)
		}
	}

	return events
}

// shouldIgnoreDir returns true for directories that should be skipped.
func (w *Watcher) shouldIgnoreDir(path string) bool {
	name := filepath.Base(path)
	for _, ignore := range w.config.IgnorePaths {
		if name == ignore {
			return true
		}
	}
	return false
}

// shouldIgnoreFile returns true for files that should be skipped (temp files,
// generated files, wrong extensions).
func (w *Watcher) shouldIgnoreFile(path string) bool {
	name := filepath.Base(path)

	// Check ignore patterns (glob).
	for _, pattern := range w.config.IgnorePatterns {
		if matched, _ := filepath.Match(pattern, name); matched {
			return true
		}
	}

	// Check extension filter.
	if len(w.config.Extensions) > 0 {
		ext := strings.ToLower(filepath.Ext(path))
		found := false
		for _, e := range w.config.Extensions {
			if ext == strings.ToLower(e) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}

	return false
}
