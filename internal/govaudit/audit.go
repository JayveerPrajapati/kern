// Package govaudit provides the tamper-evident in-memory audit log of
// every governance decision. Extracted from internal/governance (the
// audit family) when that subsystem crossed its LOC cap — extraction, not
// cap-raising, is the ledger's contract.

package govaudit

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/auditdiag"
	"github.com/JayveerPrajapati/kern/internal/auditrot"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/flock"
	"github.com/JayveerPrajapati/kern/internal/merkle"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// ValidationOutcome is Blueprint's validation result for the change this
// entry records (shared-state contract). Kern consumes it on audit
// append to invalidate context the blueprint proved stale. Field names are
// the wire format: matching the AuditEntry convention of exported Go field
// names as JSON keys (explicit tags document it).
type ValidationOutcome struct {
	Status        string   `json:"Status"` // "PASS" | "WARN" | "BLOCK" | "ERROR" | "SKIP"
	ExitCode      int      `json:"ExitCode"`
	BlockedFiles  []string `json:"BlockedFiles"`
	CorrelationID string   `json:"CorrelationID"`
	Findings      int      `json:"Findings"` // count
}

// AuditEntry records a governance-relevant event. Every decision made by the
// change firewall is captured as an audit entry.
type AuditEntry struct {
	ID        string
	Timestamp time.Time
	AgentID   string
	Action    string
	Resource  string
	Risk      domain.Risk
	Approved  bool
	Result    string // "allowed", "blocked", "pending", "denied"
	Hash      string // content hash linking this entry to the previous one (tamper chain)
	TaskID    string // Invariant 4: the task this audit entry belongs to (empty when N/A)
	// ValidationOutcome carries Blueprint's validation result for this entry.
	// It is optional (absent for legacy entries) and consumed by `kern audit
	// append` to mark blocked context stale (in-memory invalidation).
	ValidationOutcome *ValidationOutcome `json:"ValidationOutcome,omitempty"`
	// Policy identifies the policy that made the decision ("firewall",
	// "permission", "egress", ...). Optional; absent for legacy entries.
	Policy string `json:"Policy,omitempty"`
	// Reason is the human-readable justification for the decision. Optional;
	// absent for legacy entries.
	Reason string `json:"Reason,omitempty"`
	// Principal records WHO AUTHENTICATED the decision, separately from the
	// AgentID the caller DECLARED. With a shared-token or loopback console
	// there is no per-user identity, so this carries "shared-token" when a
	// bearer token gate is active or "loopback" otherwise — making
	// impersonation detectable in the audit trail (an auditor can see that
	// AgentID "root" was claimed by a principal that was NOT authenticated as
	// "root"). Optional; absent for legacy entries. The value is server-side
	// state, never client-supplied. When set it is folded into the chain
	// hash; when empty the entry hashes byte-identically to the legacy
	// format, so chains written by older binaries still verify.
	Principal string `json:"Principal,omitempty"`
	// DurationMs records how long the audited operation took (tool_call
	// entries: the wall-clock duration of the MCP tool call; other actions:
	// unset). Latency-visibility work (L3): the per-call duration is
	// queryable from the tamper-evident chain instead of only from in-memory
	// metrics aggregates. Optional; absent for legacy entries. When > 0 it
	// is folded into the chain hash; when 0 the entry hashes
	// byte-identically to the legacy format, so chains written by older
	// binaries still verify.
	DurationMs int64 `json:"DurationMs,omitempty"`
	// Annotation is a diagnostic note about HOW this entry was written when
	// the write path degraded (proceeded without the cross-process lock, or
	// the HMAC secret was unavailable so the hash fell back to plain
	// SHA-256). It is deliberately NOT part of the hash chain (writeAuditChainFields
	// never reads it), so a degraded write stays verifiable by its own
	// formula and the annotation explains why a later verification reports a
	// broken/downgraded chain instead of leaving it forensic guesswork.
	// Empty for every normal (keyed, locked) write and for all legacy
	// entries, so the persisted bytes of existing chains are unchanged.
	Annotation string `json:"Annotation,omitempty"`
}

// AuditLog records governance decisions in memory. It optionally persists each
// entry to a storage.Store with a content-hash chain for tamper detection.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
	// seq is the single authority for the "audit-N" ID sequence: the
	// persisted-tail floor (absorbed under mu) plus the allocated high-water
	// mark. It is atomic because RecordParallel's lock-free nextSeq reads it
	// concurrently with the mu-guarded absorbers (replay/persist/repair).
	seq        atomic.Int64
	atomicSeq  atomic.Int64  // atomic sequence counter for high-concurrency RecordParallel
	store      storage.Store // optional persistence; nil = in-memory only
	hashChain  string        // hash of the previous entry (tamper detection)
	lockPath   string        // cross-process advisory lock file ("" = legacy, unlocked)
	mirrorPath string        // out-of-repo JSONL mirror of persisted entries ("" = off)
	merkleTree *merkle.Tree  // incremental Merkle tree for parallel, lock-free audit verification

	// Retention (B8): the in-memory log is capped at maxAuditEntries so a
	// long-running process (the web console, the MCP server) cannot grow it
	// without bound and the per-request All() scan stays cheap. The persisted
	// chain (when a store is attached) is never trimmed — Replay() reloads
	// it — and the counters below keep lifetime totals so governance metrics
	// stay O(1) regardless of the cap. Guarded by mu.
	totalRecords int64
	blocks       int64        // Result "blocked" or "denied"
	overrides    int64        // Result "approved"
	dropped      atomic.Int64 // entries lost to persistence failure (see noteDropped)
}

// maxAuditEntries caps the in-memory audit log (B8). Older entries are
// dropped from memory but remain on disk when a store is attached; with no
// store the cap trades very old in-memory history for bounded memory.
const maxAuditEntries = 5000

// NewAuditLog creates a new in-memory audit log.
func NewAuditLog() *AuditLog {
	return &AuditLog{}
}

// WithStore attaches a storage.Store for persistence. When set, every recorded
// entry is persisted with a content hash linking it to the previous entry,
// creating a tamper-evident chain. When nil (default), the log is in-memory only.
func (l *AuditLog) WithStore(s storage.Store) *AuditLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.store = s
	return l
}

// WithLockPath attaches a blocking advisory-lock file path used to serialize
// persisted writes across processes. When empty (default), no cross-process
// lock is taken (legacy behavior). Lock the same path for every process
// writing the same store (Record, AppendExternal, RepairChain).
func (l *AuditLog) WithLockPath(path string) *AuditLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lockPath = path
	return l
}

// WithMirror attaches an out-of-repo JSONL mirror path. Every entry that
// persists to the primary store is also appended (best-effort) to the
// mirror, so a wipe of the repository — including its .kern/audit —
// leaves a survivable forensic copy under the user's home. Mirror
// failures never fail the primary persist; they are logged loudly.
func (l *AuditLog) WithMirror(path string) *AuditLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.mirrorPath = path
	return l
}

// auditMirrorDir is the base of the out-of-repo mirror tree. Package-level
// so tests can redirect it away from the real home directory.
var auditMirrorDir = func() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kern", "audit", "mirror"), nil
}

// MirrorFor derives the out-of-repo mirror path for a project audit
// directory: ~/.kern/audit/mirror/<sha256(abs(auditDir))[:16]>.jsonl —
// stable per project root, no collisions between projects. Returns ""
// when the mirror is disabled (KERN_AUDIT_MIRROR=0) or the paths cannot
// be resolved. KERN_AUDIT_MIRROR=1 forces the mirror ON even under `go
// test` (the default under tests is OFF: t.TempDir audit stores would
// otherwise mint unbounded mirror files in the real home directory).
func MirrorFor(auditDir string) string {
	switch os.Getenv("KERN_AUDIT_MIRROR") {
	case "0":
		return ""
	case "1":
		// forced on — fall through
	default:
		if testing.Testing() {
			return ""
		}
	}
	base, err := auditMirrorDir()
	if err != nil || base == "" {
		return ""
	}
	abs, err := filepath.Abs(auditDir)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(base, fmt.Sprintf("%x.jsonl", sum[:8]))
}

// storedEntriesLocked lists + write-orders persisted entries (skipping
// corrupt ones) and returns them plus the max seq. Must hold l.mu.
func (l *AuditLog) storedEntriesLocked() ([]AuditEntry, int) {
	if l.store == nil {
		return nil, 0
	}
	stored, err := l.store.List(context.Background())
	if err != nil {
		return nil, 0
	}
	// Restore WRITE order, which is what the tamper chain hashed against.
	// A chain-backed store (LogStore) reports each entry's true append ordinal
	// in WriteOrder; using it is load-bearing: a custom-ID entry appended at
	// the tail (kern audit append) really did chain after the numeric entries
	// before it, and re-sorting it to the end of the replay breaks every entry
	// written after it (live 2026-10-27 break at entry 24). Legacy per-key
	// stores report WriteOrder 0 and are ordered by the numeric audit sequence
	// (their file names are lexical, so key order scrambles the chain once the
	// log has 10+ entries).
	if _, ok := l.store.(storage.WriteOrderedStore); ok {
		sort.SliceStable(stored, func(i, j int) bool { return auditWriteOrderLess(stored[i], stored[j]) })
	} else {
		sort.SliceStable(stored, func(i, j int) bool {
			si, oki := auditSeq(strings.TrimPrefix(stored[i].Key, "audit-"))
			sj, okj := auditSeq(strings.TrimPrefix(stored[j].Key, "audit-"))
			switch {
			case oki && okj:
				return si < sj
			case oki:
				return true
			case okj:
				return false
			}
			return false
		})
	}
	var entries []AuditEntry
	maxSeq := 0
	for _, e := range stored {
		var entry AuditEntry
		if err := json.Unmarshal(e.Value, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
		if id, ok := auditSeq(entry.ID); ok && id > maxSeq {
			maxSeq = id
		}
	}
	return entries, maxSeq
}

// auditWriteOrderLess orders entries from a WriteOrderedStore: legacy per-key
// entries (WriteOrder 0) precede the append log and are ordered by their
// numeric audit sequence (custom IDs last); append-log entries follow in their
// true physical append order.
func auditWriteOrderLess(a, b storage.Entry) bool {
	if a.WriteOrder > 0 || b.WriteOrder > 0 {
		if a.WriteOrder == 0 {
			return true
		}
		if b.WriteOrder == 0 {
			return false
		}
		return a.WriteOrder < b.WriteOrder
	}
	si, oki := auditSeq(strings.TrimPrefix(a.Key, "audit-"))
	sj, okj := auditSeq(strings.TrimPrefix(b.Key, "audit-"))
	switch {
	case oki && okj:
		return si < sj
	case oki:
		return true
	case okj:
		return false
	}
	return false
}

// replayLocked loads persisted entries from the attached store into memory so
// a fresh process sees entries written by a prior one and can verify the
// tamper-evident chain. Missing or corrupt files are skipped. Entries are
// replayed in numeric audit sequence (write order), not store key order, so
// the hash chain is restored correctly. The last replayed hash becomes the
// chain head, so an entry recorded after replay chains from the persisted
// tail. It is a no-op (returning 0, nil) when no store is attached.
//
// replayLocked must be called with l.mu already held.
func (l *AuditLog) replayLocked() (int, error) {
	if l.store == nil {
		return 0, nil
	}
	entries, maxSeq := l.storedEntriesLocked()
	l.entries = append(l.entries, entries...)
	for _, e := range entries {
		if e.Hash != "" {
			l.hashChain = e.Hash
		}
	}
	if maxSeq > int(l.seq.Load()) {
		l.seq.Store(int64(maxSeq))
	}
	return len(entries), nil
}

// Replay locks the log and loads persisted entries via replayLocked.
func (l *AuditLog) Replay() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.replayLocked()
}

// auditSeq parses the numeric suffix of an auto-assigned audit ID ("audit-N").
func auditSeq(id string) (int, bool) {
	if !strings.HasPrefix(id, "audit-") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, "audit-"))
	return n, err == nil
}

// Advisory-lock retry policy (L-e): the audit lock is best-effort, but a
// bounded retry keeps the acquisition window small. The lock acquisition is
// NON-BLOCKING — a contended lock resolves within this budget instead of
// blocking a governance write forever. On exhaustion BOTH write paths fail
// closed: AppendExternal returns the error (external callers must know the
// chain link was not written; the decision they record is already durable
// elsewhere and the gap is repairable via `kern audit repair`), and the
// Record path drops the entry with the loss counted + marked in the global
// sidecar (surfaced by `kern doctor`) — an unlocked chain write can fork the
// tamper-evident chain, and a fork is indistinguishable from tampering, so a
// governance write failing loudly beats an unlocked chain.
const (
	auditLockRetries    = 3
	auditLockRetrySleep = 75 * time.Millisecond
)

// auditLockWarnOnce gates the one-time stderr warning emitted when the
// advisory audit lock could not be taken after auditLockRetries.
var auditLockWarnOnce sync.Once

// auditWriterMu serializes in-process audit writers that take the
// cross-process advisory flock. flock(2) semantics differ per platform for
// two fds of the same file within one process (independent owners on Linux,
// one process-wide lock on macOS/BSD), so concurrent goroutines in the SAME
// process can interfere with each other's lock lifecycle and emit spurious
// "could not be acquired" warnings — sequential calls must never contend
// with themselves. Holding this mutex for the whole flock lifecycle
// (acquisition → release) makes same-process writers mutually exclusive;
// the advisory flock still serializes genuinely separate processes.
var auditWriterMu sync.Mutex

// auditLockWithWriterMu takes the cross-process audit flock while holding
// auditWriterMu for the whole flock lifecycle. The returned unlock releases
// the flock AND the in-process writer mutex; it returns an error (mutex
// already released) when the lock cannot be taken, so callers can bound the
// acquisition instead of blocking on a contended lock.
func auditLockWithWriterMu(path string) (func(), error) {
	auditWriterMu.Lock()
	unlock, err := lockAuditFile(path)
	if err != nil {
		auditWriterMu.Unlock()
		return nil, err
	}
	release := unlock
	return func() {
		release()
		auditWriterMu.Unlock()
	}, nil
}

// acquireAuditLock takes the cross-process audit flock (in-process writer
// mutex held) with a bounded, non-blocking retry, failing closed on
// exhaustion (one-time warning + error naming the lock). Callers hold l.mu
// and decide how loudly to surface the failure. Nil unlock when no lock path.
func (l *AuditLog) acquireAuditLock() (func(), error) {
	if l.lockPath == "" {
		return nil, nil
	}
	// Create the parent so a fresh store self-heals instead of degrading to
	// an unlocked write (a fork of the chain is indistinguishable from
	// tampering).
	if err := os.MkdirAll(filepath.Dir(l.lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("audit lock %s: create lock directory: %w", l.lockPath, err)
	}
	var unlock func()
	var lockErr error
	for attempt := 1; attempt <= auditLockRetries; attempt++ {
		unlock, lockErr = auditLockWithWriterMu(l.lockPath)
		if lockErr == nil {
			return unlock, nil
		}
		if errors.Is(lockErr, flock.ErrLocked) && attempt < auditLockRetries {
			time.Sleep(auditLockRetrySleep)
		}
	}
	auditLockWarnOnce.Do(func() {
		log.Printf("kern governance: WARNING: audit lock %s could not be acquired after %d attempts (%v); the tamper-evident chain will NOT be written unlocked — chain gaps are repairable via `kern audit repair`", l.lockPath, auditLockRetries, lockErr)
	})
	return nil, fmt.Errorf("audit lock %s could not be acquired after %d attempts (last error: %v)", l.lockPath, auditLockRetries, lockErr)
}

// refreshTailLocked re-reads the TRUE persisted tail under the cross-process
// lock (when configured) and refreshes the in-memory chain head + sequence so
// the next write chains from its actual predecessor whoever wrote it. Returns
// the unlock func (nil when no lock is configured) and an error when the
// cross-process lock could not be acquired within the bounded retry budget —
// FAIL CLOSED: the tamper-evident chain is never degraded to unlocked writes,
// and the caller decides how loudly to surface the failure (AppendExternal
// returns the error; the Record path drops the entry with the loss counted
// and marked in the global sidecar). Must hold l.mu.
//
// When the store implements storage.TailReader (an append-only log), the tail
// is read from its single last entry in O(1) instead of re-listing every
// persisted entry on each write. The tail's Hash becomes the chain head and
// its "audit-N" ID continues the sequence, preserving ID semantics exactly
// across the legacy per-key → chain.jsonl boundary. Stores without the fast
// path fall back to the full re-list.
func (l *AuditLog) refreshTailLocked() (func(), error) {
	if l.store == nil {
		return nil, nil
	}
	unlock, lockErr := l.acquireAuditLock()
	if lockErr != nil {
		return nil, lockErr
	}
	// Fast path: an append-only store reports its tail directly, so a write
	// does not re-read the whole log. Fall through to the full re-list when
	// the tail cannot be read (empty store, corrupt tail, or a legacy scan
	// that failed).
	if tr, ok := l.store.(storage.TailReader); ok {
		if last, err := tr.LastEntry(context.Background()); err == nil {
			var tail AuditEntry
			if json.Unmarshal(last.Value, &tail) == nil {
				l.hashChain = tail.Hash
				if id, ok := auditSeq(tail.ID); ok && id > int(l.seq.Load()) {
					l.seq.Store(int64(id))
				}
				return unlock, nil
			}
		}
	}
	entries, maxSeq := l.storedEntriesLocked()
	if len(entries) > 0 {
		l.hashChain = entries[len(entries)-1].Hash
	} else {
		// The store is EMPTY: the active chain was rotated/archived. A
		// long-lived writer (an MCP server holds one AuditLog for its whole
		// lifetime) must not keep chaining to the archived tail — that makes
		// the fresh chain fail verification at entry 1. Reset the head; the
		// next entry becomes the new genesis.
		l.hashChain = ""
	}
	if maxSeq > int(l.seq.Load()) {
		l.seq.Store(int64(maxSeq))
	}
	return unlock, nil
}

// Record adds an entry to the audit log. If the entry has no ID or timestamp,
// they are assigned deterministically (by sequence) / to the current time.
func (l *AuditLog) Record(entry AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	// With a store attached, the sequence ID is assigned inside persist after
	// re-reading the true persisted tail, so it never collides with an entry
	// another process wrote in between. In-memory-only logs assign it here.
	// Every allocation goes through nextSeq — the single ID authority shared
	// with RecordParallel — so the two paths can never mint colliding
	// "audit-N" IDs (which would invalidate VerifyChain).
	if entry.ID == "" && l.store == nil {
		entry.ID = fmt.Sprintf("audit-%d", l.nextSeq())
	}
	l.entries = append(l.entries, entry)
	if l.merkleTree != nil {
		l.merkleTree.Append(hashLeaf(entry))
	}

	if l.store != nil {
		l.persist(entry)
	}
	l.noteResultAndTrimLocked(entry)
}

// noteResultAndTrimLocked updates the lifetime result counters for the
// just-appended entry and enforces the in-memory retention cap. The caller
// must hold l.mu. The most recently appended entry is never trimmed, so the
// write-back of ID/Hash in persist is safe whenever this runs after it.
func (l *AuditLog) noteResultAndTrimLocked(entry AuditEntry) {
	l.totalRecords++
	switch entry.Result {
	case "blocked", "denied":
		l.blocks++
	case "approved":
		l.overrides++
	}
	if len(l.entries) > maxAuditEntries {
		kept := l.entries[len(l.entries)-maxAuditEntries:]
		// In-place copy over the (larger) backing array. This rewrite is why
		// All()/Filter() must return copies: an aliased caller would see the
		// retained window shift under it.
		l.entries = append(l.entries[:0], kept...)
	}
}

// TotalRecords returns the lifetime number of recorded entries. Unlike Len(),
// it does not decrease when the retention cap trims old entries from memory.
func (l *AuditLog) TotalRecords() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.totalRecords
}

// BlocksCount returns the lifetime number of blocked/denied results — the
// O(1) replacement for scanning All() in governance metrics.
func (l *AuditLog) BlocksCount() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.blocks
}

// OverridesCount returns the lifetime number of approved results.
func (l *AuditLog) OverridesCount() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.overrides
}

// persist computes the tamper-evident hash chain and writes the entry to the
// store. Storage errors are tolerated: a failed persist must not crash the
// audit log, so the entry remains available in memory. When a lock path is
// configured the write is serialized across processes with an advisory lock,
// and the chain head is always re-read from the true persisted tail first so
// the entry chains from its actual predecessor whoever wrote it.
func (l *AuditLog) persist(entry AuditEntry) {
	unlock, lockErr := l.refreshTailLocked()
	if lockErr != nil {
		// FAIL CLOSED: the audit chain is the tamper-evidence mechanism and
		// must never be written unlocked — an unlocked concurrent writer can
		// fork the chain, and a fork is indistinguishable from tampering at
		// verification time. The entry stays available in memory and the
		// loss is counted + marked in the global sidecar (~/.kern/audit/
		// dropped.jsonl, surfaced by `kern doctor`) exactly like a
		// store-write failure — a governance write failing loudly beats an
		// unlocked chain.
		if entry.ID == "" {
			entry.ID = fmt.Sprintf("audit-%d", l.nextSeq())
		}
		entry.Hash = computeAuditHash(entry, l.hashChain)
		l.hashChain = entry.Hash
		last := &l.entries[len(l.entries)-1]
		last.ID = entry.ID
		last.Hash = entry.Hash
		l.noteDropped(entry, lockErr)
		return
	}
	if entry.Annotation == "" && !auditChainKeyed() {
		// The HMAC secret is unavailable in this process (sandbox HOME,
		// read-only config dir): the hash fell back to plain SHA-256.
		// Record it so the downgraded entry is distinguishable from a
		// forged one at verification time.
		entry.Annotation = "written without the audit-chain HMAC secret (hash degraded to plain SHA-256)"
	}
	if unlock != nil {
		defer l.maybeAutoRotateLocked()
		defer unlock()
	}

	// Assign the sequence ID after the tail refresh so it never collides
	// with an entry another process wrote in between. nextSeq is the single
	// ID authority (shared with RecordParallel and in-memory Record).
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("audit-%d", l.nextSeq())
	}
	// Content hash linking this entry to the previous one (tamper chain).
	entry.Hash = computeAuditHash(entry, l.hashChain)
	l.hashChain = entry.Hash
	last := &l.entries[len(l.entries)-1]
	last.ID = entry.ID
	last.Hash = entry.Hash

	data, err := json.Marshal(entry)
	if err != nil {
		// Should not happen for AuditEntry; fail softly to keep the log
		// usable — but count the loss and mark the sidecar so it is not
		// silent (surfaced in `kern doctor`).
		l.noteDropped(entry, err)
		return
	}
	key := "audit-" + entry.ID
	if err := l.store.Put(context.Background(), key, data); err != nil {
		// Log-and-skip: persistence failure must not crash the audit log,
		// but the loss must not be silent either — the entry survives only
		// in memory until the next Replay, and the drop is counted and
		// surfaced in `kern doctor` via the global sidecar.
		l.noteDropped(entry, err)
		return
	}
	// Out-of-repo mirror (best-effort, after the primary persist succeeded):
	// a repo wipe must not destroy the only copy of the governance trail.
	// The mirror line carries the entry verbatim — chain hashes stay
	// project-scoped; the mirror is a forensic copy, not a second chain.
	if l.mirrorPath != "" {
		if err := appendMirrorLine(l.mirrorPath, data); err != nil {
			log.Printf("kern governance: audit mirror append failed for %s (primary persist succeeded): %v", l.mirrorPath, err)
		}
	}
}

// appendMirrorLine appends one JSON line to the mirror file, creating the
// directory on demand. The write is advisory-only: its failure is reported
// to the caller and never propagates to the audit path.
func appendMirrorLine(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// Dropped returns the number of audit entries this log lost to persistence
// failure (marshal or store-write errors in persist). Lifetime counter for
// this process's log; the cross-process surface is the global sidecar
// written by AppendDroppedAuditMarker, which `kern doctor` reads.
func (l *AuditLog) Dropped() int64 {
	return l.dropped.Load()
}

// noteDropped counts one lost entry (best-effort: every failure inside is
// swallowed — counting a drop must never become the new crash vector) and
// appends the global sidecar marker so the loss survives the process.
func (l *AuditLog) noteDropped(entry AuditEntry, cause error) {
	l.dropped.Add(1)
	log.Printf("kern governance: audit entry %s kept in memory only (lost to persistence: %v)", entry.ID, cause)
	AppendDroppedAuditMarker(entry.ID, entry.Action, entry.Resource, cause.Error())
}

// AppendDroppedAuditMarker records one audit entry lost before it reached
// the tamper-evident chain, appending a JSON line to the global sidecar
// (~/.kern/audit/dropped.jsonl) that `kern doctor` surfaces. The sidecar
// lives OUTSIDE the failing store on purpose: a store that cannot be
// written to cannot record its own loss. Best-effort by design — this runs
// on the failure path of a failure path, so every error here is swallowed.
func AppendDroppedAuditMarker(id, action, resource, cause string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".kern", "audit")
	line, err := json.Marshal(map[string]string{
		"ts":       time.Now().UTC().Format(time.RFC3339),
		"id":       id,
		"action":   action,
		"resource": resource,
		"cause":    cause,
	})
	if err != nil {
		return
	}
	_ = os.MkdirAll(dir, 0o700)
	f, err := os.OpenFile(filepath.Join(dir, "dropped.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// AppendExternal appends an entry from an external source (e.g., Blueprint's
// audit trail) to the tamper-evident hash chain. Unlike Record, it returns an
// error if persistence fails — external callers need to know whether the chain
// link was written. The entry's ID is auto-assigned if empty (same
// "audit-N" sequence Record uses); the hash is computed over the entry plus
// the current chain head, linking it into the chain. Every persisted write
// re-reads the chain head from the true persisted tail under the
// cross-process lock (when configured), so the entry always chains from its
// actual predecessor whoever wrote it — a fresh process that skipped Replay()
// is just another stale-head writer, not a special case.
func (l *AuditLog) AppendExternal(entry AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}

	if l.store != nil {
		unlock, err := l.refreshTailLocked()
		if err != nil {
			// Fail loudly instead of hanging: the cross-process lock could
			// not be acquired within the bounded retry budget. The caller's
			// decision is already durable elsewhere (e.g. the approval is
			// persisted in .kern/approvals.json before this append runs), so
			// a missing chain link is repairable via `kern audit repair` —
			// an indefinite block is never acceptable.
			return fmt.Errorf("acquire audit lock: %w", err)
		}
		if unlock != nil {
			defer l.maybeAutoRotateLocked()
			defer unlock()
		}
	}

	if entry.ID == "" {
		entry.ID = fmt.Sprintf("audit-%d", l.nextSeq())
	}
	entry.Hash = computeAuditHash(entry, l.hashChain)
	l.entries = append(l.entries, entry)
	l.hashChain = entry.Hash
	l.noteResultAndTrimLocked(entry)

	if l.store != nil {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal audit entry: %w", err)
		}
		key := "audit-" + entry.ID
		if err := l.store.Put(context.Background(), key, data); err != nil {
			return fmt.Errorf("persist audit entry: %w", err)
		}
		// Out-of-repo mirror (best-effort, after the primary persist
		// succeeded) — same contract as persist(): a repo wipe must not
		// destroy the only copy of the governance trail. Approve/reject
		// decisions flow through this method, so they mirror too.
		if l.mirrorPath != "" {
			if err := appendMirrorLine(l.mirrorPath, data); err != nil {
				log.Printf("kern governance: audit mirror append failed for %s (primary persist succeeded): %v", l.mirrorPath, err)
			}
		}
	}
	return nil
}

// RepairChain re-chains persisted entries from the first broken link: each
// entry's Hash is recomputed against its true predecessor (content preserved;
// only chain-link hashes change). This repairs self-inflicted breaks (e.g.
// the pre-lock concurrent-writer bug) but cannot distinguish genuine
// tampering from such breaks, so it only runs on explicit user request
// (kern audit repair). Returns the number of entries re-chained.
//
// Repair FAILS CLOSED on an unacquirable lock (same contract as every other
// persisted write: an unlocked repair can fork the chain, indistinguishable
// from tampering) and is itself audited: when entries are re-chained, an
// audit.repair entry is appended to the repaired tail under the same lock, so
// the re-chain is visible in the tamper-evident trail rather than laundering
// a break into a silently-changed history.
func (l *AuditLog) RepairChain() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.store == nil {
		return 0, nil
	}

	var unlock func()
	var lockErr error
	if l.lockPath != "" {
		// Repair is a WRITE path; the fail-closed lock contract applies
		// identically. The acquisition is non-blocking and bounded (shared
		// with refreshTailLocked); on exhaustion repair is refused.
		unlock, lockErr = l.acquireAuditLock()
		if lockErr != nil {
			return 0, fmt.Errorf("refusing to repair the chain unlocked: %w", lockErr)
		}
	}
	if unlock != nil {
		defer unlock()
	}

	entries, maxSeq := l.storedEntriesLocked()
	// storedEntriesLocked already returns canonical WRITE order (append-log
	// physical order, or the numeric audit sequence for legacy per-key
	// stores) — the same order VerifyChain reads. Re-chain in exactly that
	// order; the audit.repair marker is appended at the tail below so read
	// order and hash order stay identical.
	broken := false
	prev := ""
	for _, e := range entries {
		want := computeAuditHash(e, prev)
		if e.Hash != want && e.Hash != computeAuditHashPlain(e, prev) && e.Hash != computeAuditHashLegacy(e, prev) {
			broken = true
			break
		}
		prev = e.Hash
	}
	if !broken {
		return 0, nil
	}
	// Pass 1: re-chain the existing entries in read order.
	n := 0
	prev = ""
	for i := range entries {
		want := computeAuditHash(entries[i], prev)
		if entries[i].Hash != want && entries[i].Hash != computeAuditHashPlain(entries[i], prev) && entries[i].Hash != computeAuditHashLegacy(entries[i], prev) {
			entries[i].Hash = want
			n++
		}
		prev = entries[i].Hash
	}
	if maxSeq > int(l.seq.Load()) {
		l.seq.Store(int64(maxSeq))
	}
	// Pass 2: the self-auditing marker carries the re-chain count in its
	// annotation (hashed), and inserting it can shift the Prev of entries
	// that sort after it — so build it with the final text and re-chain the
	// combined list in read order.
	r := AuditEntry{
		Timestamp:  time.Now(),
		AgentID:    "kern",
		Action:     "audit.repair",
		Resource:   "audit-chain",
		Risk:       domain.Risk{Level: domain.RiskHigh},
		Approved:   false,
		Result:     "allowed",
		Annotation: fmt.Sprintf("re-chained %d entry/entries from the first broken link", n),
	}
	r.ID = fmt.Sprintf("audit-%d", l.nextSeq())
	// storedEntriesLocked returns canonical WRITE order and the repair marker
	// is minted last with the highest sequence, so append it at the tail: the
	// read order VerifyChain uses and the hash order stay identical — including
	// when a custom external ID sits at the persisted tail (P0-2).
	all := append(append([]AuditEntry{}, entries...), r)
	prev = ""
	for i := range all {
		want := computeAuditHash(all[i], prev)
		if all[i].Hash != want && all[i].Hash != computeAuditHashPlain(all[i], prev) && all[i].Hash != computeAuditHashLegacy(all[i], prev) {
			all[i].Hash = want
			n++
		}
		prev = all[i].Hash
	}
	repair := &all[len(all)-1]

	// Persist the FULL re-chained list (marker included) as the store's sole
	// content. A per-entry Put would append a chain line AND leave a per-key
	// shadow that List returns BEFORE chain lines, rotating entries to the
	// head and breaking verification on the next read (2026-10-27 live).
	marshal := func(list []AuditEntry) ([]storage.Entry, error) {
		out := make([]storage.Entry, 0, len(list))
		for _, e := range list {
			data, err := json.Marshal(e)
			if err != nil {
				return nil, fmt.Errorf("repair chain: marshal entry %s: %w", e.ID, err)
			}
			out = append(out, storage.Entry{Key: "audit-" + e.ID, Value: data})
		}
		return out, nil
	}
	type rewriter interface {
		RewriteAll(context.Context, []storage.Entry) error
	}
	if rw, ok := l.store.(rewriter); ok {
		out, err := marshal(all)
		if err != nil {
			return 0, err
		}
		if err := rw.RewriteAll(context.Background(), out); err != nil {
			return 0, fmt.Errorf("repair chain: rewrite store: %w", err)
		}
	} else {
		for _, e := range all {
			data, err := json.Marshal(e)
			if err != nil {
				return 0, fmt.Errorf("repair chain: marshal entry %s: %w", e.ID, err)
			}
			if err := l.store.Put(context.Background(), "audit-"+e.ID, data); err != nil {
				return 0, fmt.Errorf("repair chain: persist entry %s: %w", e.ID, err)
			}
		}
	}

	l.entries = all
	l.hashChain = prev
	l.noteResultAndTrimLocked(*repair)
	if l.mirrorPath != "" {
		if data, err := json.Marshal(*repair); err == nil {
			if err := appendMirrorLine(l.mirrorPath, data); err != nil {
				log.Printf("kern governance: audit mirror append failed for repair entry %s: %v", repair.ID, err)
			}
		}
	}
	return n - 1, nil
}

// auditChainSecretPath returns the path of the audit-chain HMAC secret,
// stored OUTSIDE the client-governed workspace next to the exec-approval
// secret (same R1 pattern as exec_approval.go), so a governed client with
// write access to the audit store cannot forge the chain. It is a
// package-level func so tests can redirect it away from the real user
// config dir.
var auditChainSecretPath = func() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kern", "audit-chain.key"), nil
}

// The audit secret is generated once per process and cached; the file
// persists across processes so chains written by one process verify in a
// later one (the same design as the exec-approval secret).
var (
	auditChainSecretOnce sync.Once
	auditChainSecretKey  []byte
	auditChainSecretErr  error
)

// auditChainSecret returns the 32-byte HMAC secret, creating it once at
// <UserConfigDir>/kern/audit-chain.key (dir 0700, file 0600) on first use.
// It deliberately uses its OWN key file (not exec-approval.key) so the audit
// chain's integrity is not coupled to the exec-approval key's lifecycle.
// The result is cached — success and failure alike — so callers can decide
// how to degrade deterministically.
func auditChainSecret() ([]byte, error) {
	auditChainSecretOnce.Do(func() {
		p, err := auditChainSecretPath()
		if err != nil {
			auditChainSecretErr = err
			return
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			auditChainSecretErr = err
			return
		}
		if data, rerr := os.ReadFile(p); rerr == nil {
			if len(data) != 32 {
				auditChainSecretErr = fmt.Errorf("audit-chain secret %s is %d bytes, want 32; refusing to use it", p, len(data))
				return
			}
			auditChainSecretKey = data
			return
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			auditChainSecretErr = err
			return
		}
		// O_CREATE|O_EXCL so two processes racing the first-use generation
		// cannot cross-key: the loser re-reads the winner's key.
		f, werr := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if werr != nil {
			if errors.Is(werr, fs.ErrExist) && func() bool {
				data, rerr := os.ReadFile(p)
				if rerr == nil && len(data) == 32 {
					auditChainSecretKey = data
					return true
				}
				return false
			}() {
				return
			}
			auditChainSecretErr = werr
			return
		}
		if _, err := f.Write(key); err != nil {
			_ = f.Close()
			auditChainSecretErr = err
			return
		}
		if err := f.Close(); err != nil {
			auditChainSecretErr = err
			return
		}
		auditChainSecretKey = key
	})
	return auditChainSecretKey, auditChainSecretErr
}

// auditChainSecretDegraded warns once per process when the HMAC secret is
// unavailable and hashing degrades to the plain formula.
var auditChainSecretDegraded sync.Once

// auditHashFormatVersion versions the audit-chain HMAC serialization so a
// future framing change is distinguishable from tampering by construction.
const auditHashFormatVersion = 1

// computeAuditHash computes the chain hash of the audit entry content
// concatenated with the previous entry's hash, creating a tamper-evident
// chain: modifying any entry invalidates all subsequent hashes. It is
// HMAC-SHA256 keyed by a secret stored OUTSIDE the workspace, so the chain
// is tamper-evident against FULL rewrites too — an attacker with write
// access to the audit store cannot recompute the chain without the secret.
// When the secret is unavailable (read-only user config dir, sandbox) it
// degrades to the plain formula (computeAuditHashPlain) and warns once:
// the audit log must not lose entries over an unavailable key, matching the
// persist path's tolerate-storage-errors policy. Chains persisted before
// HMAC still verify via the plain/legacy fallbacks in VerifyChainReport.
func computeAuditHash(e AuditEntry, prevHash string) string {
	key, err := auditChainSecret()
	if err != nil {
		auditChainSecretDegraded.Do(func() {
			log.Printf("kern governance: audit-chain HMAC secret unavailable (%v); audit hashes fall back to plain SHA-256 — full-chain-rewrite tamper protection is OFF", err)
		})
		return computeAuditHashPlain(e, prevHash)
	}
	h := hmac.New(sha256.New, key)
	// The format version is the first MAC input so framing changes and
	// tampering are distinguishable by construction.
	_, _ = h.Write([]byte{auditHashFormatVersion})
	writeAuditChainFields(h, e, prevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// writeAuditChainFields writes the audit entry content plus the previous
// chain hash into h using the canonical pipe-separated framing shared by the
// HMAC and plain formulas. ValidationOutcome is part of a persisted entry, so
// it must be covered too, or it could be modified without breaking
// VerifyChain. Entries with a nil ValidationOutcome serialize byte-identically
// to the legacy format, so chains recorded by older versions still verify.
// Principal follows the same conditional-clause pattern: an empty Principal
// (all legacy entries) keeps the bytes identical to the pre-Principal
// format, while a set Principal is folded into the hash so the
// authenticated-identity field is tamper-evident like every other field.
func writeAuditChainFields(h io.Writer, e AuditEntry, prevHash string) {
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s|%v|%v|%v|%s|%s", prevHash, e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	if e.ValidationOutcome != nil {
		_, _ = fmt.Fprintf(h, "|%s|%d|%s|%s|%d", e.ValidationOutcome.Status, e.ValidationOutcome.ExitCode, strings.Join(e.ValidationOutcome.BlockedFiles, ","), e.ValidationOutcome.CorrelationID, e.ValidationOutcome.Findings)
	}
	if e.Principal != "" {
		_, _ = fmt.Fprintf(h, "|%s", e.Principal)
	}
	if e.DurationMs > 0 {
		_, _ = fmt.Fprintf(h, "|%d", e.DurationMs)
	}
}

// computeAuditHashPlain recomputes the pre-HMAC chain formula: plain SHA-256
// over the full entry content (ValidationOutcome clause included when
// present) — exactly what computeAuditHash computed before HMAC-SHA256. It
// is the degraded-mode computation when the audit secret is unavailable and
// a verification fallback for chains persisted by pre-HMAC binaries.
func computeAuditHashPlain(e AuditEntry, prevHash string) string {
	h := sha256.New()
	writeAuditChainFields(h, e, prevHash)
	return hex.EncodeToString(h.Sum(nil))
}

// IntegrityMode reports which hash mechanism currently protects the audit
// chain: "hmac" when the out-of-workspace HMAC secret is available, or
// "plain" when it is not and hashes silently degrade to plain SHA-256
// (full-chain-rewrite tamper protection off). Callers — e.g. `kern audit` —
// surface the degraded mode so it is never silent; the write path never
// fails over an unavailable key (entries-not-lost posture is deliberate).
func (l *AuditLog) IntegrityMode() string {
	if _, err := auditChainSecret(); err != nil {
		return "plain"
	}
	return "hmac"
}

// Len returns the total number of audit entries in memory.
func (l *AuditLog) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// All returns all audit entries in insertion order. The returned slice is a
// copy: the retention trim rewrites the internal backing array in place
// (noteResultAndTrimLocked), so callers must never observe or mutate it.
func (l *AuditLog) All() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

// Filter returns entries matching the given agent ID. An empty agentID matches
// all entries. The returned slice is a copy, like All().
func (l *AuditLog) Filter(agentID string) []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if agentID == "" {
		return slices.Clone(l.entries)
	}
	var out []AuditEntry
	for _, e := range l.entries {
		if e.AgentID == agentID {
			out = append(out, e)
		}
	}
	return out
}

// VerifyChainReport verifies the hash chain and reports how it fails.
// firstBroken is the index of the first entry whose stored hash does not
// match the recomputed chain (-1 when the whole chain verifies); verified is
// the number of entries that DO verify while chaining via stored hashes.
// Interpretation:
//   - verified == total      → chain intact.
//   - verified == 0          → nothing verifies, not even the first entry
//     against an empty chain head: the signature of entries persisted by an
//     older kern version with a different hash format — but also the exact
//     signature of a deliberate full-chain rewrite, so callers must warn,
//     not dismiss it as a calm migration note.
//   - 0 < verified < total   → a genuine break at firstBroken (tampering, or
//     a mix of versions): entries after the modified one typically still
//     verify because the chain links via stored hashes.
//
// Format strength is monotonic: legacy < plain < HMAC, and a chain only ever
// gets stronger as kern is upgraded. Once a keyed (HMAC) entry has been seen,
// a later entry that verifies only under a weaker formula is a downgrade and
// counts as broken — otherwise anyone able to write the store could append or
// splice unkeyed entries behind keyed ones, because the plain formulas need
// no secret. (Entries written while the key was unavailable are also flagged;
// they stay unkeyed — `kern audit repair` re-chains only genuinely broken
// links, never re-signs an intact entry of unknown origin.)
func (l *AuditLog) VerifyChainReport() (firstBroken, verified int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	firstBroken, verified, _ = l.verifyChainLocked()
	return firstBroken, verified
}

// ChainTiers counts how each audit entry verified. Keyed entries are bound to
// the out-of-workspace HMAC secret; Plain and Legacy entries use unkeyed
// SHA-256 formulas that anyone with write access to the store can recompute,
// so they prove ordering, not authenticity. Downgraded entries verified only
// under a weaker formula than an earlier entry and are counted as Broken too.
type ChainTiers struct {
	Total      int `json:"total"`
	Keyed      int `json:"keyed"`
	Plain      int `json:"plain"`
	Legacy     int `json:"legacy"`
	Broken     int `json:"broken"`
	Downgraded int `json:"downgraded"`
	// TailDowngraded counts unkeyed entries written AFTER the last keyed
	// entry — the only ones whose authenticity nothing later in the chain
	// attests. A downgraded entry followed by a keyed entry is still
	// tamper-evident (that later entry's Prev hash covers it), so it must
	// not trigger the startup warning on every command; FirstTailDowngraded
	// is its 0-based index (-1 when none).
	TailDowngraded      int `json:"tail_downgraded"`
	FirstTailDowngraded int `json:"first_tail_downgraded"`
}

// Unkeyed is the number of verified entries that carry no secret-bound proof.
func (t ChainTiers) Unkeyed() int { return t.Plain + t.Legacy }

// DowngradeExplainsBreak reports whether every broken entry in the chain is
// a format downgrade — i.e. it verified, but only under a weaker unkeyed
// formula than an earlier keyed entry. That is the exact signature of
// entries appended while the HMAC secret was unavailable (an
// environment-degraded server: no $HOME, container, cron — NEW-5): the
// chain still links via stored hashes and ordering holds; only the
// authenticity of those entries is unproven. Callers use it to report the
// degradation ACCURATELY instead of framing it as tampering ("FAILED"),
// while a genuine splice (broken entries beyond the downgrades, or a chain
// that verifies nowhere) still fails loudly.
func (t ChainTiers) DowngradeExplainsBreak() bool {
	return t.Broken > 0 && t.Broken == t.Downgraded && t.Keyed > 0
}

// ChainTierReport verifies the chain exactly like VerifyChainReport and
// returns the per-format breakdown. An in-memory-only log reports every entry
// as keyed-equivalent (nothing is persisted to forge).
func (l *AuditLog) ChainTierReport() ChainTiers {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _, t := l.verifyChainLocked()
	return t
}

const (
	auditTierLegacy = iota + 1
	auditTierPlain
	auditTierKeyed
)

// maybeAutoRotateLocked archives the active chain when it crosses the
// retention policy, then resets the in-memory head. Must hold l.mu and NOT
// the cross-process lock (deferred ordering releases it first).
func (l *AuditLog) maybeAutoRotateLocked() {
	if l == nil || l.lockPath == "" || l.store == nil {
		return
	}
	root, size, ok := auditrot.Policy(filepath.Dir(l.lockPath))
	if !ok {
		return
	}
	keep := auditrot.Keep()
	if _, err := ArchiveAuditStore(root, keep); err != nil {
		log.Printf("kern governance: audit auto-rotate failed (chain left intact): %v", err)
		return
	}
	l.entries = nil
	l.hashChain = ""
	log.Printf("kern governance: audit chain auto-rotated at %d bytes; kept %d archives", size, keep)
}

// ArchiveAuditStore moves the active audit store (chain.jsonl plus the
// per-key files that shadow chain lines) into <auditDir>/archive-<UTC
// timestamp>/ and keeps only the newest `keep` archives (keep <= 0 keeps all).
// It takes the SAME cross-process audit lock writers use, so a rotation never
// splits an entry or races an append. The active chain restarts empty and the
// next append starts a fresh, fully keyed chain; archived entries stay on disk
// and readable. This is the bounded-retention answer to an audit chain that
// grows without limit (23 MB / 53k entries observed 2026-10-26).
func ArchiveAuditStore(root string, keep int) (string, error) {
	auditDir := filepath.Join(root, ".kern", "audit")
	if err := os.MkdirAll(auditDir, 0o700); err != nil {
		return "", err
	}
	unlock, err := auditLockWithWriterMu(filepath.Join(auditDir, ".lock"))
	if err != nil {
		return "", fmt.Errorf("audit rotate: acquire lock: %w", err)
	}
	defer unlock()
	entries, err := os.ReadDir(auditDir)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(auditDir, "archive-"+time.Now().UTC().Format("20060102-150405.000000000"))
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", err
	}
	moved := 0
	for _, e := range entries {
		// Move the chain files AND the verification run directories; skip
		// the lock, existing archives, and unknown operator directories.
		if e.Name() == ".lock" || strings.HasPrefix(e.Name(), "archive-") {
			continue
		}
		if e.IsDir() && !auditdiag.IsRunDir(e.Name()) {
			continue
		}
		if err := os.Rename(filepath.Join(auditDir, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return "", fmt.Errorf("audit rotate: move %s: %w", e.Name(), err)
		}
		moved++
	}
	if keep > 0 {
		auditrot.Prune(auditDir, keep)
	}
	if moved == 0 {
		_ = os.Remove(dest)
		return "", fmt.Errorf("audit rotate: no audit store files in %s", auditDir)
	}
	return dest, nil
}

func (l *AuditLog) verifyChainLocked() (firstBroken, verified int, tiers ChainTiers) {
	tiers.Total = len(l.entries)
	if l.store == nil {
		tiers.Keyed = len(l.entries)
		return -1, len(l.entries), tiers
	}
	firstBroken = -1
	tiers.FirstTailDowngraded = -1
	var prevHash string
	strongest := 0
	lastKeyed := -1
	downgraded := make([]bool, 0, len(l.entries))
	for i, e := range l.entries {
		// Accept any of the three chain formulas: HMAC-SHA256 (current), the
		// pre-HMAC plain formula (chains persisted by the immediately
		// preceding binaries), and the legacy plain formula WITHOUT the
		// ValidationOutcome clause (entries persisted by the in-transition
		// binary BEFORE ValidationOutcome was folded into the hash, and all
		// older chains). Each fallback covers a strict subset of the fields
		// the current formula covers, so accepting it verifies exactly what
		// was verifiable when the entry was written. Strongest match wins so
		// an entry that happens to verify under several formulas is credited
		// to the keyed one.
		tier := 0
		switch e.Hash {
		case computeAuditHash(e, prevHash):
			tier = auditTierKeyed
			if !auditChainKeyed() {
				// computeAuditHash degraded to the plain formula: the match
				// proves nothing a keyless writer could not have produced.
				tier = auditTierPlain
			}
		case computeAuditHashPlain(e, prevHash):
			tier = auditTierPlain
		case computeAuditHashLegacy(e, prevHash):
			tier = auditTierLegacy
		}
		if tier != 0 && tier < strongest {
			tiers.Downgraded++
			downgraded = append(downgraded, true)
			tier = 0
		} else {
			downgraded = append(downgraded, false)
		}
		if tier == 0 {
			tiers.Broken++
			if firstBroken < 0 {
				firstBroken = verified
			}
		} else {
			verified++
			if tier == auditTierKeyed {
				lastKeyed = i
			}
			if tier > strongest {
				strongest = tier
			}
			switch tier {
			case auditTierKeyed:
				tiers.Keyed++
			case auditTierPlain:
				tiers.Plain++
			default:
				tiers.Legacy++
			}
		}
		prevHash = e.Hash
	}
	for i := lastKeyed + 1; i < len(downgraded); i++ {
		if downgraded[i] {
			tiers.TailDowngraded++
			if tiers.FirstTailDowngraded < 0 {
				tiers.FirstTailDowngraded = i
			}
		}
	}
	return firstBroken, verified, tiers
}

// auditChainKeyed reports whether the HMAC secret is available, i.e. whether
// computeAuditHash currently produces a keyed hash.
func auditChainKeyed() bool {
	_, err := auditChainSecret()
	return err == nil
}

// computeAuditHashLegacy recomputes an entry hash WITHOUT the
// ValidationOutcome clause — the formula used by older binaries
// extended the chain to cover it. Used only as a verification fallback for
// entries persisted during that transition window (their stored hash cannot
// match the modern formula by construction).
func computeAuditHashLegacy(e AuditEntry, prevHash string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s|%s|%s|%s|%s|%v|%v|%v|%s|%s", prevHash, e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	return hex.EncodeToString(h.Sum(nil))
}

// hashLeaf computes the leaf SHA-256 hash for an AuditEntry. It is the
// governance-side serialization of an audit entry into a Merkle leaf; the
// merkle.Tree itself is generic over string leaves.
func hashLeaf(e AuditEntry) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "leaf|%s|%s|%s|%s|%v|%v|%v|%s|%s", e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	return hex.EncodeToString(h.Sum(nil))
}

// VerifyChain reports whether the audit log's hash chain is intact (no entries
// have been tampered with). Returns true when the log is in-memory only (no
// persistence) or when all hashes verify.
func (l *AuditLog) VerifyChain() bool {
	firstBroken, _ := l.VerifyChainReport()
	return firstBroken < 0
}

// MerkleRoot returns the current Merkle tree root hash for the audit log.
func (l *AuditLog) MerkleRoot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.merkleTree == nil {
		l.initMerkleTreeLocked()
	}
	return l.merkleTree.Root()
}

// VerifyMerkle verifies the tree-root integrity of all entries in the audit log.
func (l *AuditLog) VerifyMerkle() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return true
	}
	var leaves []string
	for _, e := range l.entries {
		leaves = append(leaves, hashLeaf(e))
	}
	expected := merkle.ComputeRoot(leaves)
	if l.merkleTree == nil {
		l.initMerkleTreeLocked()
	}
	return expected == l.merkleTree.Root()
}

func (l *AuditLog) initMerkleTreeLocked() {
	l.merkleTree = merkle.NewTree()
	for _, e := range l.entries {
		l.merkleTree.Append(hashLeaf(e))
	}
}

// nextSeq allocates a sequence number atomically without holding l.mu. It is
// the SINGLE authority for the "audit-N" ID sequence: every ID-assigning path
// (Record, persist, AppendExternal, RecordParallel) allocates through it, so
// no two paths — the mu-guarded writers and the lock-free parallel path — can
// mint colliding IDs. The persisted-tail floor (l.seq) is absorbed under mu
// by replay/refresh/repair; nextSeq always allocates above max(atomicSeq,
// l.seq).
func (l *AuditLog) nextSeq() int64 {
	for {
		cur := l.atomicSeq.Load()
		lSeq := l.seq.Load()
		base := cur
		if lSeq > base {
			base = lSeq
		}
		next := base + 1
		if l.atomicSeq.CompareAndSwap(cur, next) {
			return next
		}
	}
}

// RecordParallel allows concurrent agents to commit audit entries in parallel,
// offloading CPU-intensive leaf hashing outside the mutex critical section,
// and rolling entries into the incremental Merkle tree without lock bottlenecks.
// Entries are persisted to the store exactly like Record (L-d), so parallel
// entries are crash-durable rather than memory-only.
func (l *AuditLog) RecordParallel(entry AuditEntry) string {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	if entry.ID == "" {
		seq := l.nextSeq()
		entry.ID = fmt.Sprintf("audit-%d", seq)
	}

	// Compute leaf hash outside the lock in parallel across calling goroutines.
	leaf := hashLeaf(entry)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.merkleTree == nil {
		l.initMerkleTreeLocked()
	}
	root := l.merkleTree.Append(leaf)
	l.entries = append(l.entries, entry)
	if l.store != nil {
		// Persist exactly like Record: parallel entries must be crash-
		// durable too. persist recomputes entry.Hash from the true
		// persisted tail and writes ID/Hash back into l.entries, so the
		// chain head must NOT be advanced before this call (that would
		// chain the entry on itself). Same pattern as Record: called
		// while holding l.mu, and persist's refreshTailLocked requires it.
		l.persist(entry)
	} else {
		entry.Hash = computeAuditHash(entry, l.hashChain)
		l.hashChain = entry.Hash
		// persist normally writes ID/Hash back into the slice; mirror it
		// for the in-memory-only path.
		l.entries[len(l.entries)-1] = entry
	}
	l.noteResultAndTrimLocked(entry)
	return root
}
