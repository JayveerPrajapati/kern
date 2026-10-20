// Package servedledger implements the per-session served ledger (anti-stall
// campaign, Phase 0 / B1): a bounded, mutex-guarded record of read-tool
// responses already served within one MCP session. When the same
// (tool, view-defining args) is requested again, the MCP layer answers with
// a one-line pointer instead of re-emitting the payload — protecting the
// agent's context window and teaching the etag in-band. Deterministic,
// stdlib-only, no network, no LLM in the loop.
//
// The ledger is keyed by a session key (agent_id|task, else "default") so
// distinct agents/tasks never see each other's serves, and bounded per
// session at MaxEntriesPerSession entries with FIFO eviction. The MCP server
// owns one *Ledger per Server instance (one Server = one MCP session), so
// repeats within a session are caught without cross-session pollution; the
// package-level Default backs the Record/Lookup/Reset/Summary API surface
// for callers that want process-wide semantics (later campaign phases read
// the ledger through it).
package servedledger

import (
	"os"
	"strconv"
	"sync"
)

// MaxEntriesPerSession bounds one session's FIFO ledger: 512 entries covers
// a working session of distinct reads; the oldest serves are evicted first.
const MaxEntriesPerSession = 512

// Entry is one served-response record: the call sequence number that
// produced the serve, the etag minted for it ("" when none was recorded),
// an approximate payload size, and the file references extracted from the
// served text. ByteLen is the canonical-args byte length — a deterministic
// proxy for call/view complexity (the full payload is deliberately not
// threaded through Record; the session-semantic keying stays minimal).
type Entry struct {
	CallSeq int
	ETag    string
	ByteLen int
	Files   []string // root-relative file paths referenced by the served text (B3)
}

// Ledger is the mutex-guarded served ledger. The zero value is not ready to
// use — construct with New.
type Ledger struct {
	mu       sync.Mutex
	seq      int // process-wide call sequence counter (monotonic across sessions)
	sessions map[string]*session
}

// session is one session key's FIFO ledger: canonical keys in insertion
// order (oldest first) plus their entries.
type session struct {
	order []string
	byKey map[string]Entry
}

// New returns an empty ledger.
func New() *Ledger {
	return &Ledger{sessions: map[string]*session{}}
}

// Default is the process-wide served ledger backing the package API surface.
// The MCP server keeps its own per-session instance (one Server = one MCP
// session); Default exists for callers that want process-wide semantics.
var Default = New()

// Enabled reports whether the served ledger is active. KERN_SERVED_LEDGER=0
// disables it; any other value (including unset) enables it — the same
// opt-out discipline as the D1 cache's KERN_MCP_CACHE=0 knob.
func Enabled() bool {
	return os.Getenv("KERN_SERVED_LEDGER") != "0"
}

// keyFor joins the session key, tool name and canonical args into one map
// key. Canonical args come from json.Marshal, which escapes control
// characters, so they never contain a literal NUL byte — the \x00 separator
// cannot collide with the components.
func keyFor(sessionKey, tool, canonicalArgs string) string {
	return sessionKey + "\x00" + tool + "\x00" + canonicalArgs
}

// Record stores (or refreshes) one serve for (sessionKey, tool,
// canonicalArgs), returning the call sequence number assigned to it.
// Re-recording the same key updates the etag, files and sequence number (a
// re-serve after a content change mints a fresh etag). Evicts the oldest
// entries when the session's FIFO cap is exceeded. Concurrent calls are
// safe.
func (l *Ledger) Record(sessionKey, tool, canonicalArgs, etag string, files []string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	seq := l.seq
	sess := l.sessions[sessionKey]
	if sess == nil {
		sess = &session{byKey: map[string]Entry{}}
		l.sessions[sessionKey] = sess
	}
	key := keyFor(sessionKey, tool, canonicalArgs)
	if _, exists := sess.byKey[key]; !exists {
		sess.order = append(sess.order, key)
	}
	sess.byKey[key] = Entry{CallSeq: seq, ETag: etag, ByteLen: len(canonicalArgs), Files: files}
	for len(sess.order) > MaxEntriesPerSession {
		oldest := sess.order[0]
		sess.order = sess.order[1:]
		delete(sess.byKey, oldest)
	}
	return seq
}

// Lookup reports whether (sessionKey, tool, canonicalArgs) was already
// served this session, returning the one-line pointer. The pointer is
// computed at lookup time, so message-format changes need no entry rewrites.
// ok is false when the key was never recorded (or was evicted at the FIFO
// cap).
func (l *Ledger) Lookup(sessionKey, tool, canonicalArgs string) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	sess := l.sessions[sessionKey]
	if sess == nil {
		return "", false
	}
	e, ok := sess.byKey[keyFor(sessionKey, tool, canonicalArgs)]
	if !ok {
		return "", false
	}
	return Pointer(e.CallSeq, e.ETag), true
}

// Pointer renders the one-line already-served pointer for a recorded serve.
// The ", etag E" segment and the "re-send with etag E" clause are omitted
// entirely when no etag was recorded.
func Pointer(callSeq int, etag string) string {
	if etag == "" {
		return "already served this session (call #" + strconv.Itoa(callSeq) + ") — re-send with the previous etag to confirm unchanged, or vary the view (tier/lines/max_tokens), or no_cache=1 to re-emit"
	}
	return "already served this session (call #" + strconv.Itoa(callSeq) + ", etag " + etag + ") — re-send with etag " + etag + " to confirm unchanged, or vary the view (tier/lines/max_tokens), or no_cache=1 to re-emit"
}

// Reset clears every recorded serve for sessionKey.
func (l *Ledger) Reset(sessionKey string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessions, sessionKey)
}

// FilesCovered aggregates the file references recorded across a session's
// entries: true for every root-relative file that was served (directly or
// as a referenced path) at least once. It is the B3 context-sufficiency
// data source — kern_plan asks "which of these affected files have I
// already been served?".
func (l *Ledger) FilesCovered(sessionKey string) map[string]bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]bool{}
	sess := l.sessions[sessionKey]
	if sess == nil {
		return out
	}
	for _, e := range sess.byKey {
		for _, f := range e.Files {
			out[f] = true
		}
	}
	return out
}

// Summary returns the number of entries currently tracked for sessionKey and
// the number of distinct sessions tracked in the ledger.
func (l *Ledger) Summary(sessionKey string) (entries, sessions int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if sess := l.sessions[sessionKey]; sess != nil {
		entries = len(sess.byKey)
	}
	return entries, len(l.sessions)
}
