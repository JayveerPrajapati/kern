// Package audit provides the tamper-evident in-memory audit log of every
// governance decision.

package governance

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
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
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
	merkleTree *MerkleTree   // incremental Merkle tree for parallel, lock-free audit verification

	// Retention (B8): the in-memory log is capped at maxAuditEntries so a
	// long-running process (the web console, the MCP server) cannot grow it
	// without bound and the per-request All() scan stays cheap. The persisted
	// chain (when a store is attached) is never trimmed — Replay() reloads
	// it — and the counters below keep lifetime totals so governance metrics
	// stay O(1) regardless of the cap. Guarded by mu.
	totalRecords int64
	blocks       int64 // Result "blocked" or "denied"
	overrides    int64 // Result "approved"
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

// storedEntriesLocked lists + numeric-sorts persisted entries (skipping
// corrupt ones) and returns them plus the max seq. Must hold l.mu.
func (l *AuditLog) storedEntriesLocked() ([]AuditEntry, int) {
	if l.store == nil {
		return nil, 0
	}
	stored, err := l.store.List(context.Background())
	if err != nil {
		return nil, 0
	}
	// Restore write order: the store lists keys lexically
	// ("audit-audit-1", "audit-audit-10", ...), which scrambles the tamper
	// chain for any log with 10+ entries. Sorting by the numeric audit
	// sequence reconstructs the original chain so VerifyChain can pass.
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

// refreshTailLocked re-reads the TRUE persisted tail under the cross-process
// lock (when configured) and refreshes the in-memory chain head + sequence so
// the next write chains from its actual predecessor whoever wrote it. Returns
// the unlock func (nil when no lock is configured or it could not be taken);
// callers must invoke it after their write completes. Must hold l.mu.
//
// When the store implements storage.TailReader (an append-only log), the tail
// is read from its single last entry in O(1) instead of re-listing every
// persisted entry on each write. The tail's Hash becomes the chain head and
// its "audit-N" ID continues the sequence, preserving ID semantics exactly
// across the legacy per-key → chain.jsonl boundary. Stores without the fast
// path fall back to the full re-list.
func (l *AuditLog) refreshTailLocked() func() {
	if l.store == nil {
		return nil
	}
	var unlock func()
	if l.lockPath != "" {
		var err error
		unlock, err = lockAuditFile(l.lockPath)
		if err != nil {
			// The lock is advisory and failure-tolerant: proceed unlocked
			// rather than crash the write. The chain may break again, but
			// the entry is never lost.
			unlock = nil
		}
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
				return unlock
			}
		}
	}
	entries, maxSeq := l.storedEntriesLocked()
	if len(entries) > 0 {
		l.hashChain = entries[len(entries)-1].Hash
	}
	if maxSeq > int(l.seq.Load()) {
		l.seq.Store(int64(maxSeq))
	}
	return unlock
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
	unlock := l.refreshTailLocked()
	if unlock != nil {
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
		// Should not happen for AuditEntry; fail softly to keep the log usable.
		return
	}
	key := "audit-" + entry.ID
	if err := l.store.Put(context.Background(), key, data); err != nil {
		// Log-and-skip: persistence failure must not crash the audit log,
		// but the loss must not be silent either — the entry survives only
		// in memory until the next Replay.
		log.Printf("kern governance: audit entry %s kept in memory only (store write failed: %v)", entry.ID, err)
	}
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
		unlock := l.refreshTailLocked()
		if unlock != nil {
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
	}
	return nil
}

// RepairChain re-chains persisted entries from the first broken link:
// each entry's Hash is recomputed against its true predecessor (content is
// preserved; only the chain-link hashes change). This repairs self-inflicted
// breaks (e.g. the pre-lock concurrent-writer bug). It cannot distinguish
// genuine tampering from such breaks, so it must only run on explicit user
// request (kern audit repair). Returns the number of entries re-chained.
func (l *AuditLog) RepairChain() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.store == nil {
		return 0, nil
	}

	var unlock func()
	if l.lockPath != "" {
		var err error
		unlock, err = lockAuditFile(l.lockPath)
		if err != nil {
			// Lock is advisory and failure-tolerant: repair without it is
			// best-effort rather than a hard failure.
			unlock = nil
		}
	}
	if unlock != nil {
		defer unlock()
	}

	entries, maxSeq := l.storedEntriesLocked()
	prev := ""
	n := 0
	modified := make([]bool, len(entries))
	for i, e := range entries {
		want := computeAuditHash(e, prev)
		// An entry that verifies under any of the three formulas (HMAC, the
		// pre-HMAC plain formula, or the legacy formula) is intact — leave
		// it untouched so repair stays minimal (only genuinely broken links
		// are re-chained).
		if e.Hash != want && e.Hash != computeAuditHashPlain(e, prev) && e.Hash != computeAuditHashLegacy(e, prev) {
			e.Hash = want
			modified[i] = true
			n++
		}
		prev = e.Hash
		entries[i] = e
	}

	// Persist: when the store supports an atomic full rewrite (LogStore),
	// write the repaired entry list as the store's sole content. A per-entry
	// Put would APPEND a new chain.jsonl line per repaired key without
	// removing prior lines for that key, leaving duplicate entries that
	// break every subsequent chain walk.
	type rewriter interface {
		RewriteAll(context.Context, []storage.Entry) error
	}
	if rw, ok := l.store.(rewriter); ok {
		out := make([]storage.Entry, 0, len(entries))
		for _, e := range entries {
			data, err := json.Marshal(e)
			if err != nil {
				return n, fmt.Errorf("repair chain: marshal entry %s: %w", e.ID, err)
			}
			out = append(out, storage.Entry{Key: "audit-" + e.ID, Value: data})
		}
		if err := rw.RewriteAll(context.Background(), out); err != nil {
			return n, fmt.Errorf("repair chain: rewrite store: %w", err)
		}
	} else {
		for i, e := range entries {
			if !modified[i] {
				continue
			}
			data, err := json.Marshal(e)
			if err != nil {
				return n, fmt.Errorf("repair chain: marshal entry %s: %w", e.ID, err)
			}
			key := "audit-" + e.ID
			if err := l.store.Put(context.Background(), key, data); err != nil {
				return n, fmt.Errorf("repair chain: persist entry %s: %w", e.ID, err)
			}
		}
	}

	l.entries = entries
	l.hashChain = prev
	if maxSeq > int(l.seq.Load()) {
		l.seq.Store(int64(maxSeq))
	}
	return n, nil
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
func (l *AuditLog) VerifyChainReport() (firstBroken, verified int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store == nil {
		return -1, len(l.entries)
	}
	firstBroken = -1
	var prevHash string
	for _, e := range l.entries {
		// Accept any of the three chain formulas: HMAC-SHA256 (current), the
		// pre-HMAC plain formula (chains persisted by the immediately
		// preceding binaries), and the legacy plain formula WITHOUT the
		// ValidationOutcome clause (entries persisted by the in-transition
		// binary BEFORE ValidationOutcome was folded into the hash, and all
		// older chains). Each fallback covers a strict subset of the fields
		// the current formula covers, so accepting it verifies exactly what
		// was verifiable when the entry was written — nothing that was ever
		// protected is weakened.
		if e.Hash != computeAuditHash(e, prevHash) &&
			e.Hash != computeAuditHashPlain(e, prevHash) &&
			e.Hash != computeAuditHashLegacy(e, prevHash) {
			if firstBroken < 0 {
				firstBroken = verified
			}
		} else {
			verified++
		}
		prevHash = e.Hash
	}
	return firstBroken, verified
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

// VerifyChain reports whether the audit log's hash chain is intact (no entries
// have been tampered with). Returns true when the log is in-memory only (no
// persistence) or when all hashes verify.
func (l *AuditLog) VerifyChain() bool {
	firstBroken, _ := l.VerifyChainReport()
	return firstBroken < 0
}

// MerkleTree is an incremental Merkle Tree for parallel, tamper-evident audit logging.
type MerkleTree struct {
	mu     sync.RWMutex
	leaves []string
	levels [][]string // levels[0] is leaves; levels[d] stores pairwise completed nodes
	root   string
}

// NewMerkleTree initializes an empty incremental Merkle tree.
func NewMerkleTree() *MerkleTree {
	return &MerkleTree{
		levels: make([][]string, 0),
	}
}

// hashLeaf computes the leaf SHA-256 hash for an AuditEntry.
func hashLeaf(e AuditEntry) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "leaf|%s|%s|%s|%s|%v|%v|%v|%s|%s", e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	return hex.EncodeToString(h.Sum(nil))
}

// hashNode computes the parent SHA-256 hash from left and right child hashes.
func hashNode(left, right string) string {
	h := sha256.New()
	h.Write([]byte(left + ":" + right))
	return hex.EncodeToString(h.Sum(nil))
}

// computeMerkleRoot computes the root hash from a slice of leaf hashes.
func computeMerkleRoot(leaves []string) string {
	if len(leaves) == 0 {
		return ""
	}
	current := make([]string, len(leaves))
	copy(current, leaves)
	for len(current) > 1 {
		var next []string
		for i := 0; i < len(current); i += 2 {
			if i+1 < len(current) {
				next = append(next, hashNode(current[i], current[i+1]))
			} else {
				next = append(next, hashNode(current[i], current[i]))
			}
		}
		current = next
	}
	return current[0]
}

// Append inserts a leaf hash into the incremental Merkle tree in O(log N) time
// and updates the cached tree root.
func (m *MerkleTree) Append(leaf string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.leaves = append(m.leaves, leaf)
	if len(m.levels) == 0 {
		m.levels = append(m.levels, nil)
	}

	level := 0
	m.levels[level] = append(m.levels[level], leaf)

	// Propagate completed pairs up the levels (amortized O(1))
	for len(m.levels[level])%2 == 0 {
		idx := len(m.levels[level])
		parent := hashNode(m.levels[level][idx-2], m.levels[level][idx-1])
		level++
		if level >= len(m.levels) {
			m.levels = append(m.levels, nil)
		}
		m.levels[level] = append(m.levels[level], parent)
	}

	// Roll up unpaired branch nodes to compute the root in O(log N)
	m.root = m.computeRootFromLevelsLocked()
	return m.root
}

// computeRootFromLevelsLocked folds any trailing odd (unpaired) nodes across tree levels.
func (m *MerkleTree) computeRootFromLevelsLocked() string {
	n := len(m.leaves)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return m.leaves[0]
	}

	var folded string
	hasFolded := false
	top := len(m.levels) - 1

	for d := 0; d < top; d++ {
		lvl := m.levels[d]
		if len(lvl)%2 == 1 {
			unpaired := lvl[len(lvl)-1]
			if !hasFolded {
				folded = hashNode(unpaired, unpaired)
				hasFolded = true
			} else {
				folded = hashNode(unpaired, folded)
			}
		} else if hasFolded {
			folded = hashNode(folded, folded)
		}
	}

	topNode := m.levels[top][len(m.levels[top])-1]
	if !hasFolded {
		return topNode
	}
	return hashNode(topNode, folded)
}

// Root returns the current Merkle root hash in O(1) time.
func (m *MerkleTree) Root() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root
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
	expected := computeMerkleRoot(leaves)
	if l.merkleTree == nil {
		l.initMerkleTreeLocked()
	}
	return expected == l.merkleTree.Root()
}

func (l *AuditLog) initMerkleTreeLocked() {
	l.merkleTree = NewMerkleTree()
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
	entry.Hash = computeAuditHash(entry, l.hashChain)
	l.hashChain = entry.Hash
	l.entries = append(l.entries, entry)
	l.noteResultAndTrimLocked(entry)
	return root
}
