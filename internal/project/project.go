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

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// Session bundles a project root with its on-demand symbol index and a stats
// session identity. It is safe for concurrent use.
type Session struct {
	mu      sync.Mutex
	Root    string
	Session string
	ix      *index.Index
}

// New returns a Session for root. An empty root resolves to the current
// directory. session is the optional identity used when recording stats.
func New(root, session string) *Session {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	return &Session{Root: root, Session: session}
}

// Index returns the symbol index for the session's root. A cached index is
// reused while fresh; a stale or missing index is rebuilt and persisted so the
// session always reflects the current tree (see index.Stale).
func (s *Session) Index() (*index.Index, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ix != nil && !s.ix.Stale() {
		return s.ix, nil
	}
	if ix, err := index.Load(s.Root); err == nil && ix != nil && !ix.Stale() {
		s.ix = ix
		return ix, nil
	}
	ix, err := index.Build(s.Root)
	if err != nil {
		return nil, err
	}
	s.ix = ix
	_ = ix.Save()
	return ix, nil
}

// Invalidate drops the cached index so the next Index() call rebuilds it.
func (s *Session) Invalidate() {
	s.mu.Lock()
	s.ix = nil
	s.mu.Unlock()
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
