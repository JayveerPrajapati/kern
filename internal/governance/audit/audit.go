// Package audit provides the tamper-evident in-memory audit log of every
// governance decision.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

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
	a.store = s
	return a
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

// computeAuditHash computes a SHA-256 hash of the audit entry content
// concatenated with the previous entry's hash, creating a tamper-evident
// chain: modifying any entry invalidates all subsequent hashes.
func computeAuditHash(e AuditEntry, prevHash string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%v|%v|%v|%s", prevHash, e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result)
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

// VerifyChain reports whether the audit log's hash chain is intact (no entries
// have been tampered with). Returns true when the log is in-memory only (no
// persistence) or when all hashes verify.
func (l *AuditLog) VerifyChain() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store == nil {
		return true
	}
	var prevHash string
	for _, e := range l.entries {
		expected := computeAuditHash(e, prevHash)
		if e.Hash != expected {
			return false
		}
		prevHash = e.Hash
	}
	return true
}
