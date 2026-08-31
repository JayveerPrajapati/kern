// Package audit provides the tamper-evident in-memory audit log of every
// governance decision.
package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// ValidationOutcome is Blueprint's validation result for the change this
// entry records (P0.4 shared-state contract). Kern consumes it on audit
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
	// append` to mark blocked context stale (in-memory invalidation, P0.4).
	ValidationOutcome *ValidationOutcome `json:"ValidationOutcome,omitempty"`
}

// AuditLog records governance decisions in memory. It optionally persists each
// entry to a storage.Store with a content-hash chain for tamper detection.
type AuditLog struct {
	mu        sync.Mutex
	entries   []AuditEntry
	seq       int
	store     storage.Store // optional persistence; nil = in-memory only
	hashChain string        // hash of the previous entry (tamper detection)
}

// NewAuditLog creates a new in-memory audit log.
func NewAuditLog() *AuditLog {
	return &AuditLog{}
}

// WithStore attaches a storage.Store for persistence. When set, every recorded
// entry is persisted with a content hash linking it to the previous entry,
// creating a tamper-evident chain. When nil (default), the log is in-memory only.
func (a *AuditLog) WithStore(s storage.Store) *AuditLog {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.store = s
	return a
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
	stored, err := l.store.List(context.Background())
	if err != nil {
		return 0, err
	}
	// Restore write order before replaying: the store lists keys lexically
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
	n := 0
	for _, e := range stored {
		var entry AuditEntry
		if err := json.Unmarshal(e.Value, &entry); err != nil {
			continue
		}
		l.entries = append(l.entries, entry)
		if entry.Hash != "" {
			l.hashChain = entry.Hash
		}
		if id, ok := auditSeq(entry.ID); ok && id > l.seq {
			l.seq = id
		}
		n++
	}
	return n, nil
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

// Record adds an entry to the audit log. If the entry has no ID or timestamp,
// they are assigned deterministically (by sequence) / to the current time.
func (l *AuditLog) Record(entry AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry.ID == "" {
		l.seq++
		entry.ID = fmt.Sprintf("audit-%d", l.seq)
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	l.entries = append(l.entries, entry)

	if l.store != nil {
		l.persist(entry)
	}
}

// persist computes the tamper-evident hash chain and writes the entry to the
// store. Storage errors are tolerated: a failed persist must not crash the
// audit log, so the entry remains available in memory.
func (l *AuditLog) persist(entry AuditEntry) {
	// Content hash linking this entry to the previous one (tamper chain).
	entry.Hash = computeAuditHash(entry, l.hashChain)
	l.hashChain = entry.Hash
	l.entries[len(l.entries)-1].Hash = entry.Hash

	data, err := json.Marshal(entry)
	if err != nil {
		// Should not happen for AuditEntry; fail softly to keep the log usable.
		return
	}
	key := "audit-" + entry.ID
	if err := l.store.Put(context.Background(), key, data); err != nil {
		// Log-and-skip: persistence failure must not crash the audit log.
		_ = err
	}
}

// AppendExternal appends an entry from an external source (e.g., Blueprint's
// audit trail) to the tamper-evident hash chain. Unlike Record, it returns an
// error if persistence fails — external callers need to know whether the chain
// link was written. The entry's ID is auto-assigned if empty (same
// "audit-N" sequence Record uses); the hash is computed over the entry plus
// the current chain head, linking it into the chain. It self-heals: when a
// store is attached but the chain head has not been loaded yet (a fresh
// process that skipped Replay()), the persisted chain is replayed first so the
// new entry chains from the persisted tail instead of an empty hash.
func (l *AuditLog) AppendExternal(entry AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Self-heal: a store attached but never replayed means l.hashChain is
	// empty/stale. Load the persisted chain head first so this entry links
	// onto the real chain tail.
	if l.store != nil && l.hashChain == "" && len(l.entries) == 0 {
		if _, err := l.replayLocked(); err != nil {
			return fmt.Errorf("replay audit log before append: %w", err)
		}
	}

	if entry.ID == "" {
		l.seq++
		entry.ID = fmt.Sprintf("audit-%d", l.seq)
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now()
	}
	entry.Hash = computeAuditHash(entry, l.hashChain)
	l.entries = append(l.entries, entry)
	l.hashChain = entry.Hash

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

// computeAuditHash computes a SHA-256 hash of the audit entry content
// concatenated with the previous entry's hash, creating a tamper-evident
// chain: modifying any entry invalidates all subsequent hashes.
func computeAuditHash(e AuditEntry, prevHash string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%v|%v|%v|%s|%s", prevHash, e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	return hex.EncodeToString(h.Sum(nil))
}

// All returns all audit entries in insertion order.
func (l *AuditLog) All() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.entries
}

// Filter returns entries matching the given agent ID. An empty agentID matches
// all entries.
func (l *AuditLog) Filter(agentID string) []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if agentID == "" {
		return l.entries
	}
	var out []AuditEntry
	for _, e := range l.entries {
		if e.AgentID == agentID {
			out = append(out, e)
		}
	}
	return out
}

// FilterByTask returns entries matching the given task ID (Invariant 4). An
// empty taskID returns entries with no task association.
func (l *AuditLog) FilterByTask(taskID string) []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []AuditEntry
	for _, e := range l.entries {
		if e.TaskID == taskID {
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
		if e.Hash != computeAuditHash(e, prevHash) {
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

// VerifyChain reports whether the audit log's hash chain is intact (no entries
// have been tampered with). Returns true when the log is in-memory only (no
// persistence) or when all hashes verify.
func (l *AuditLog) VerifyChain() bool {
	firstBroken, _ := l.VerifyChainReport()
	return firstBroken < 0
}
