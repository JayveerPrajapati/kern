// Package project provides a Session facade that bundles a project root with
// its lazily-loaded, auto-refreshed symbol index and the session identity used
// when recording optimization stats. Tools and CLI commands use it instead of
// re-resolving root + index + session independently on every call, so a
// session shares one in-memory index (rebuilt only when stale) and records
// telemetry under a consistent identity.
package project

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// Session bundles a project root with its on-demand symbol index and a stats
// session identity. It is safe for concurrent use.
type Session struct {
	mu         sync.Mutex
	Root       string
	Session    string
	ix         *index.Index
	staleUntil time.Time // cooldown: skip staleness walk until this time
	stale      bool      // mark index stale on file-event notification
	watcher    *fileWatcher
	saveWG     sync.WaitGroup // B6: in-flight background index saves (drained by Close)
	// B1 single-flight state: one rebuild runs at a time, OFF the session
	// lock (stale-while-revalidate). cond wakes callers that arrived before
	// any index existed and must wait for the first build; buildResult /
	// buildErr carry the finished build's outcome to those waiters. They are
	// deliberately separate from s.ix — Invalidate() nulls s.ix, and a waiter
	// must still receive the build that just completed.
	rebuilding  bool
	buildResult *index.Index
	buildErr    error
	cond        *sync.Cond
	// Derived computation cache: deterministic functions of the index, cleared
	// in Invalidate(). Populated lazily by the accessor methods below.
	arch         *intel.Architecture
	communities  []intel.Community
	hubs         []intel.Hub
	hubsLimit    int
	bridges      []intel.Bridge
	bridgesLimit int
}

// New returns a Session for root. An empty root resolves to the current
// directory. session is the optional identity used when recording stats.
// When a native file-event tool (inotifywait/fswatch) is available, a background
// watcher is started that invalidates the cached index on file changes for
// near-real-time freshness.
func New(root, session string) *Session {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	s := &Session{Root: root, Session: session}
	s.cond = sync.NewCond(&s.mu)
	s.watcher = newFileWatcher(root, func(string) { s.Invalidate() })
	return s
}

// Close stops the background file watcher, if any. Safe to call multiple times.
// The MCP server calls this on shutdown.
func (s *Session) Close() {
	s.mu.Lock()
	if s.watcher != nil {
		s.watcher.Stop()
	}
	s.mu.Unlock()
	// B6: drain in-flight background saves so the persisted copy is complete
	// before the process exits.
	s.saveWG.Wait()
}

// Index returns the symbol index for the session's root. A cached index is
// reused while fresh; a stale or missing index is rebuilt and persisted so the
// session always reflects the current tree (see index.Stale).
// To avoid a full filesystem walk on every MCP tool call, the staleness check
// is rate-limited: once it returns "fresh", the next check is skipped for
// staleCooldown (1 second by default), so burst tool calls reuse the cached
// index without re-walking disk.
// Index returns the symbol index for the session's root. A cached index is
// reused while fresh; a stale or missing index is rebuilt and persisted so the
// session always reflects the current tree (see index.Stale).
// To avoid a full filesystem walk on every MCP tool call, the staleness check
// is rate-limited: once it returns "fresh", the next check is skipped for
// staleCooldown (1 second by default), so burst tool calls reuse the cached
// index without re-walking disk.
//
// B1 — stale-while-revalidate: the rebuild runs OFF the session lock, so the
// first tool call after an edit no longer blocks ALL other tool calls for the
// full rebuild (30-90s on large repos). The triggering caller waits for the
// fresh index; concurrent callers are served the stale cached snapshot
// immediately. A single rebuild is in flight at any time (single-flight);
// callers that arrive before any index exists wait on the condition variable.
func (s *Session) Index() (*index.Index, error) {
	s.mu.Lock()
	// A file-event notification (or explicit invalidation) marks the index
	// stale, bypassing the cooldown so we never serve stale code.
	if s.stale {
		s.stale = false
	}
	if s.ix != nil && !s.stale && time.Now().Before(s.staleUntil) {
		ix := s.ix
		s.mu.Unlock()
		return ix, nil
	}
	if s.ix != nil && !s.stale && !s.ix.Stale() {
		s.staleUntil = time.Now().Add(s.freshnessCooldown())
		ix := s.ix
		s.mu.Unlock()
		return ix, nil
	}

	if s.rebuilding {
		if s.ix != nil {
			// Stale-while-revalidate: serve the stale snapshot now; the
			// in-flight rebuild atomically swaps in a fresh index when it
			// finishes.
			ix := s.ix
			s.mu.Unlock()
			return ix, nil
		}
		// Nothing to serve yet (first build): wait for the in-flight one.
		for s.rebuilding {
			s.cond.Wait()
		}
		// Consume the dedicated build result: s.ix may legitimately be nil
		// here (an Invalidate() between the broadcast and this read), but
		// the waiter still receives the index that was just built.
		ix, err := s.buildResult, s.buildErr
		s.mu.Unlock()
		return ix, err
	}

	// We are the rebuilder.
	s.rebuilding = true
	root := s.Root
	s.mu.Unlock()

	ix, err := s.rebuildIndex(root)

	s.mu.Lock()
	s.rebuilding = false
	s.buildErr = err
	s.buildResult = ix
	if err == nil && ix != nil {
		s.ix = ix
		s.staleUntil = time.Now().Add(s.freshnessCooldown())
		s.stale = false
		// Clear the derived computation caches so they rebuild with the
		// fresh index (same fields Invalidate clears).
		s.arch = nil
		s.communities = nil
		s.hubs = nil
		s.hubsLimit = 0
		s.bridges = nil
		s.bridgesLimit = 0
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	return ix, err
}

// rebuildIndex runs the load/update/build cascade OFF the session lock. It
// never touches the session's served index: any previous index it uses as
// the incremental-Update base is loaded from disk privately (Update mutates
// its prev — initMaps/reindexByFile — and the served in-memory copy may still
// be read by concurrent tool calls, so passing it would be a data race; a
// shadow refresh therefore falls back to a full Build, which reads only
// disk). Prefer an incremental Update over a full Build whenever a previous
// index loads cleanly from a store: Update re-parses only changed files,
// reusing symbols and edges of unchanged ones. Any Update failure (or no
// loadable previous index) falls back to a full Build. The explicit
// `kern index` CLI command is unaffected (it calls index.Build directly).
func (s *Session) rebuildIndex(root string) (*index.Index, error) {
	var prev *index.Index
	if index.SQLiteEnabled() {
		// SQLite is the persistent store for concurrent access (WAL). Prefer
		// it over the JSON cache; rebuild when absent or stale.
		if ix, err := index.LoadSQLite(root); err == nil && ix != nil {
			if !ix.Stale() {
				return ix, nil
			}
			if prev == nil {
				prev = ix
			}
		}
	}
	if ix, err := index.Load(root); err == nil && ix != nil {
		if !ix.Stale() {
			return ix, nil
		}
		if prev == nil {
			prev = ix
		}
	}
	var ix *index.Index
	if prev != nil {
		if uix, uerr := index.Update(root, prev); uerr == nil && uix != nil {
			ix = uix
		}
	}
	if ix == nil {
		var berr error
		ix, berr = index.Build(root)
		if berr != nil {
			return nil, berr
		}
	}
	if index.SQLiteEnabled() {
		// Persist to SQLite for concurrent access; the JSON cache remains as
		// a fallback for builds without the sqlite tag.
		if serr := index.SaveSQLite(root, ix); serr == nil {
			return ix, nil
		}
	}
	// B6: persist off the caller's critical path. json.Marshal of the whole
	// index + fsync can cost hundreds of ms on large repos. The save is
	// atomic (unique temp file + rename), so a process that exits before it
	// lands leaves the previous persisted copy (rebuilt on next start), and
	// Close() drains in-flight saves before the process goes away.
	s.saveWG.Add(1)
	go func() {
		defer s.saveWG.Done()
		if err := ix.Save(); err != nil {
			log.Printf("project: persist index %s: %v", root, err)
		}
	}()
	return ix, nil
}

// staleCooldown is how long to trust a "fresh" result before re-checking disk
// when no file-event watcher is active (the polling fallback: the walk is the
// only change detector, so it must stay tight).
const staleCooldown = 1 * time.Second

// staleCooldownNative is the freshness window when a file-event watcher is
// active (B5). File events mark the index stale immediately — bypassing the
// cooldown — so the full staleness walk + git proof only runs as a periodic
// safety net for events the watcher missed, instead of once per second.
const staleCooldownNative = 5 * time.Second

// freshnessCooldown returns the staleness re-check window for this session:
// relaxed when a file-event watcher is running (events bypass the window via
// Invalidate), tight otherwise (B5).
func (s *Session) freshnessCooldown() time.Duration {
	if s.watcher != nil {
		return staleCooldownNative
	}
	return staleCooldown
}

// CachedIndex returns the current in-memory index pointer and whether it is present.
// It does not trigger a disk rebuild or file-walk, making it ideal for non-blocking health checks.
func (s *Session) CachedIndex() (*index.Index, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ix, s.ix != nil && !s.stale
}

// Invalidate drops the cached index so the next Index() call rebuilds it.
// Called by the file-event watcher when source files change.
func (s *Session) Invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ix = nil
	s.staleUntil = time.Time{}
	s.stale = true
	// Clear derived computation cache so it rebuilds with the fresh index.
	s.arch = nil
	s.communities = nil
	s.hubs = nil
	s.hubsLimit = 0
	s.bridges = nil
	s.bridgesLimit = 0
}

// Architecture returns the cached architecture analysis, computing it once
// and reusing it until the index is invalidated.
func (s *Session) Architecture(ix *index.Index) intel.Architecture {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.arch != nil {
		return *s.arch
	}
	a := intel.AnalyzeArchitecture(ix)
	s.arch = &a
	return a
}

// CommunitiesList returns the cached community list, computing it once.
func (s *Session) CommunitiesList(ix *index.Index) []intel.Community {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.communities != nil {
		return s.communities
	}
	s.communities = intel.Communities(ix)
	return s.communities
}

// HubsList returns the cached hub list for the given limit. A different limit
// forces recompute (hubs are sorted by score; a smaller limit is a prefix but
// we recompute to be safe with the sort).
func (s *Session) HubsList(ix *index.Index, limit int) []intel.Hub {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hubs != nil && s.hubsLimit == limit {
		return s.hubs
	}
	s.hubs = intel.Hubs(ix, limit)
	s.hubsLimit = limit
	return s.hubs
}

// BridgesList returns the cached bridge list for the given limit.
func (s *Session) BridgesList(ix *index.Index, limit int) []intel.Bridge {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bridges != nil && s.bridgesLimit == limit {
		return s.bridges
	}
	s.bridges = intel.Bridges(ix, limit)
	s.bridgesLimit = limit
	return s.bridges
}

// Recorder returns a stats recorder rooted in the local cache, or nil when
// recording is unavailable (e.g. no writable cache directory).
func (s *Session) Recorder() *stats.Recorder {
	rec, err := stats.NewRecorder()
	if err != nil {
		return nil
	}
	return rec
}

// Record writes a stats entry tagged with the session identity. Recording is
// best-effort: failures (cache dir unwritable) are silently ignored.
func (s *Session) Record(op stats.Operation, source, model string, before, after int) {
	rec := s.Recorder()
	if rec == nil {
		return
	}
	_ = rec.Record(stats.Entry{
		Session:      s.Session,
		Operation:    op,
		Source:       source,
		Model:        model,
		BeforeTokens: before,
		AfterTokens:  after,
	})
}
