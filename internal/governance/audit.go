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
	"sync/atomic"
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
	mu         sync.Mutex
	entries    []AuditEntry
	seq        int
	atomicSeq  int64         // atomic sequence counter for high-concurrency RecordParallel
	store      storage.Store // optional persistence; nil = in-memory only
	hashChain  string        // hash of the previous entry (tamper detection)
	lockPath   string        // cross-process advisory lock file ("" = legacy, unlocked)
	merkleTree *MerkleTree   // incremental Merkle tree for parallel, lock-free audit verification
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

// WithLockPath attaches a blocking advisory-lock file path used to serialize
// persisted writes across processes. When empty (default), no cross-process
// lock is taken (legacy behavior). Lock the same path for every process
// writing the same store (Record, AppendExternal, RepairChain).
func (a *AuditLog) WithLockPath(path string) *AuditLog {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lockPath = path
	return a
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
	if maxSeq > l.seq {
		l.seq = maxSeq
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
				if id, ok := auditSeq(tail.ID); ok && id > l.seq {
					l.seq = id
				}
				return unlock
			}
		}
	}
	entries, maxSeq := l.storedEntriesLocked()
	if len(entries) > 0 {
		l.hashChain = entries[len(entries)-1].Hash
	}
	if maxSeq > l.seq {
		l.seq = maxSeq
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
	if entry.ID == "" && l.store == nil {
		l.seq++
		entry.ID = fmt.Sprintf("audit-%d", l.seq)
	}
	l.entries = append(l.entries, entry)
	if l.merkleTree != nil {
		l.merkleTree.Append(hashLeaf(entry))
	}

	if l.store != nil {
		l.persist(entry)
	}
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
	// with an entry another process wrote in between.
	if entry.ID == "" {
		l.seq++
		entry.ID = fmt.Sprintf("audit-%d", l.seq)
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
		// Log-and-skip: persistence failure must not crash the audit log.
		_ = err
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
		l.seq++
		entry.ID = fmt.Sprintf("audit-%d", l.seq)
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
	for i, e := range entries {
		want := computeAuditHash(e, prev)
		if e.Hash != want {
			e.Hash = want
			data, err := json.Marshal(e)
			if err != nil {
				return n, fmt.Errorf("repair chain: marshal entry %s: %w", e.ID, err)
			}
			key := "audit-" + e.ID
			if err := l.store.Put(context.Background(), key, data); err != nil {
				return n, fmt.Errorf("repair chain: persist entry %s: %w", e.ID, err)
			}
			n++
		}
		prev = e.Hash
		entries[i] = e
	}

	l.entries = entries
	l.hashChain = prev
	if maxSeq > l.seq {
		l.seq = maxSeq
	}
	return n, nil
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
	fmt.Fprintf(h, "leaf|%s|%s|%s|%s|%v|%v|%v|%s|%s", e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
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

// Leaves returns a copy of all leaf hashes in the Merkle tree.
func (m *MerkleTree) Leaves() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]string, len(m.leaves))
	copy(cp, m.leaves)
	return cp
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

// nextSeq allocates a sequence number atomically without holding l.mu.
func (l *AuditLog) nextSeq() int64 {
	for {
		cur := atomic.LoadInt64(&l.atomicSeq)
		lSeq := int64(l.seq)
		base := cur
		if lSeq > base {
			base = lSeq
		}
		next := base + 1
		if atomic.CompareAndSwapInt64(&l.atomicSeq, cur, next) {
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
	return root
}
