// Package project provides a Session facade that bundles a project root with
// its lazily-loaded, auto-refreshed symbol index and the session identity used
// when recording optimization stats. Tools and CLI commands use it instead of
// re-resolving root + index + session independently on every call, so a
// session shares one in-memory index (rebuilt only when stale) and records
// telemetry under a consistent identity.
package project

import (
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
	s.watcher = newFileWatcher(root, func(string) { s.Invalidate() })
	return s
}

// Close stops the background file watcher, if any. Safe to call multiple times.
// The MCP server calls this on shutdown.
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watcher != nil {
		s.watcher.Stop()
	}
}

// Index returns the symbol index for the session's root. A cached index is
// reused while fresh; a stale or missing index is rebuilt and persisted so the
// session always reflects the current tree (see index.Stale).
// To avoid a full filesystem walk on every MCP tool call, the staleness check
// is rate-limited: once it returns "fresh", the next check is skipped for
// staleCooldown (1 second by default), so burst tool calls reuse the cached
// index without re-walking disk.
func (s *Session) Index() (*index.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// A file-event notification (or explicit invalidation) marks the index
	// stale, bypassing the cooldown so we never serve stale code.
	if s.stale {
		s.stale = false
	}
	if s.ix != nil && !s.stale && time.Now().Before(s.staleUntil) {
		return s.ix, nil
	}
	if s.ix != nil && !s.stale && !s.ix.Stale() {
		s.staleUntil = time.Now().Add(staleCooldown)
		return s.ix, nil
	}
	if index.SQLiteEnabled() {
		// SQLite is the persistent store for concurrent access (WAL). Prefer
		// it over the JSON cache; rebuild when absent or stale.
		if ix, err := index.LoadSQLite(s.Root); err == nil && ix != nil && !ix.Stale() {
			s.ix = ix
			s.staleUntil = time.Now().Add(staleCooldown)
			s.stale = false
			return ix, nil
		}
	}
	if ix, err := index.Load(s.Root); err == nil && ix != nil && !ix.Stale() {
		s.ix = ix
		s.staleUntil = time.Now().Add(staleCooldown)
		s.stale = false
		return ix, nil
	}
	ix, err := index.Build(s.Root)
	if err != nil {
		return nil, err
	}
	s.ix = ix
	s.staleUntil = time.Now().Add(staleCooldown)
	s.stale = false
	if index.SQLiteEnabled() {
		// Persist to SQLite for concurrent access; the JSON cache remains as
		// a fallback for builds without the sqlite tag.
		if serr := index.SaveSQLite(s.Root, ix); serr == nil {
			return ix, nil
		}
	}
	_ = ix.Save()
	return ix, nil
}

// staleCooldown is how long to trust a "fresh" result before re-checking disk.
const staleCooldown = 1 * time.Second

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
