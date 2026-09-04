// Package audit implements the Blueprint validation audit trail (P1-1).
//
// Every validation that flows through the canonical pipeline
// (BlueprintService.Validate) writes one JSONL record to
// .blueprint/audit/audit.jsonl. Because the write happens inside the service
// itself, every adapter (CLI check/ci, MCP tools, git hook, watcher) is
// covered automatically. The trail is best-effort: a failed audit write must
// never fail a validation (same philosophy as metrics).
//
// Records carry findings METADATA only — rule id, severity, category, file,
// line — never messages, evidence, or snippets (redaction invariant). Each
// record is self-hashed: Hash is the sha256 of the canonical JSON of the
// record with Hash cleared, so tampering with any field breaks the hash.
//
// In addition to the local JSONL, every Write best-effort links a mapped entry
// into kern's tamper-evident hash chain (E-3) by shelling out to
// `kern audit append --root <repo>` with the record's fields piped on stdin.
// Kern owns the chain (each entry's hash covers the previous entry's hash, so
// reordering or deletion is detected); the local JSONL stays the authoritative
// fast mirror. Linking is best-effort in both directions: a missing kern
// binary, a failed subprocess, or a non-zero exit only writes a warning to
// stderr and never fails validation (see kern_link.go).
package audit

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// FindingMeta is the redacted metadata of one finding: rule, severity,
// category, and location. It deliberately omits message, explanation,
// suggested_fix, evidence, and snippets (redaction invariant). Suppression
// state (P1-2) is included so a suppression lift is itself auditable.
type FindingMeta struct {
	RuleID   string          `json:"rule_id"`
	Severity domain.Severity `json:"severity"`
	Category domain.Category `json:"category"`
	File     string          `json:"file"`
	Line     int             `json:"line"`
	// Suppressed records that this finding was lifted by a reviewed,
	// expiring suppression; Owner is the routing target from owners.yaml.
	Suppressed bool   `json:"suppressed,omitempty"`
	Owner      string `json:"owner,omitempty"`
}

// SummaryMeta is the aggregated rollup of a validation run.
type SummaryMeta struct {
	Total    int `json:"total"`
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Blocks   int `json:"blocks"`
	Skipped  int `json:"skipped"`
}

// Record is one JSONL line in the audit trail. Timestamp marshals as
// RFC3339Nano (time.Time's default JSON encoding).
type Record struct {
	// Kind distinguishes validation records from approval-decision records
	// (P1.3). Empty (omitted) means a validation record — the historical
	// default — so existing records keep their shape. `blueprint approve`
	// writes records with Kind "approval-decision" so the two-person-rule
	// decisions are auditable alongside validations.
	Kind              string                    `json:"kind,omitempty"`
	CorrelationID     string                    `json:"correlation_id"`
	Timestamp         time.Time                 `json:"timestamp"`
	Source            domain.Source             `json:"source"`
	AgentID           string                    `json:"agent_id,omitempty"`
	Operation         domain.Operation          `json:"operation"`
	RepoRoot          string                    `json:"repo_root"`
	Status            domain.Status             `json:"status"`
	ExitCode          int                       `json:"exit_code"`
	Summary           SummaryMeta               `json:"summary"`
	Findings          []FindingMeta             `json:"findings"`
	DurationMs        int64                     `json:"duration_ms"`
	ChecksSkipped     []string                  `json:"checks_skipped,omitempty"`
	ContextProvenance *domain.ContextProvenance `json:"context_provenance,omitempty"`
	Hash              string                    `json:"hash"`
	// PreviousHash (P1.4) is the Hash of the immediately preceding record in
	// the local audit chain. Empty (omitted) for the genesis record and for
	// pre-P1.4 records. Each record's Hash covers PreviousHash, so altering
	// any record breaks every subsequent record's hash — the chain is
	// tamper-evident locally (previously only kern's side chained). Backward
	// compatible: legacy records without the field are treated as "" and the
	// chain starts from the first record that carries it.
	PreviousHash string `json:"previous_hash,omitempty"`
}

// Writer appends self-hashed JSONL records to a single audit file and, when a
// kern binary is reachable, best-effort links each record into kern's
// tamper-evident chain (see linkToKernChain).
type Writer struct {
	mu         sync.Mutex
	path       string
	kernBinary string // forced kern binary path (tests/embedders); "" = resolve per call

	// lastHash reports the Hash of the most recently written record (for
	// LastHash, which the P1.4 receipt generator stamps as the chain
	// endpoint). Write re-reads the ACTUAL file tail under the flock on every
	// append — another Writer or process may have appended since, so the cache
	// is a report of what THIS writer last wrote, never trusted to pick the
	// next record's predecessor. lastKernChainHash caches the hash kern
	// returned for the most recent chain link ("" when kern is unavailable or
	// returned nothing parseable).
	lastHash          string
	lastKernChainHash string
}

// NewWriter returns an audit Writer that appends to path.
func NewWriter(path string) *Writer {
	return &Writer{path: path}
}

// WithKernBinary forces the kern binary path used for chain linking. When
// unset, Write resolves it per call from KERN_BINARY, $PATH, then
// ../kern/bin/kern. Tests inject a fake binary this way.
func (w *Writer) WithKernBinary(path string) *Writer {
	w.kernBinary = path
	return w
}

// Write appends one self-hashed JSONL record. The self-hash is computed FIRST
// over the canonical JSON of the record with Hash cleared, then the final
// record (with Hash set) is marshaled and appended as a single line.
//
// P1.4 hash chaining: the record's Hash covers PreviousHash (the preceding
// record's Hash), so tampering with any record breaks every subsequent
// record's hash. The first record in a file — and legacy pre-P1.4 records —
// carry PreviousHash "" (genesis/anchor).
//
// Concurrency (H7): writes are serialized twice — by the in-process mutex
// (same Writer) and by an exclusive flock on <path>.lock (other processes, or
// other Writer instances in the same process). The flock is held across the
// last-hash read AND the append, so two processes cannot fork the chain by
// both reading genesis and then both appending. The appended line is fsynced
// before the lock is released, so a crash cannot leave a torn partial line
// that breaks verification.
//
// Best-effort contract: callers must be able to ignore the returned error — a
// failed audit write must never fail validation.
func (w *Writer) Write(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// The audit directory must exist before the lock file (sibling of the
	// audit file) can be created.
	if err := os.MkdirAll(filepath.Dir(w.path), 0o755); err != nil {
		return err
	}

	// Cross-process serialization: every append to this file must observe the
	// previous record's hash, so the lock covers the read AND the write.
	unlock, err := lockAuditFile(w.path)
	if err != nil {
		return err
	}
	defer unlock()

	// Chain onto the previous record's hash. The actual file tail is re-read
	// under the flock on EVERY write: another Writer (or process) may have
	// appended since our last write, so the cached lastHash can be stale and
	// must not be trusted to pick the predecessor — trusting it forks the
	// chain (two records chaining onto the same predecessor). Re-reading also
	// lets a fresh Writer on an existing audit.jsonl continue the chain
	// instead of restarting at genesis.
	prevHash := w.readLastHash()
	w.lastHash = prevHash
	r.PreviousHash = prevHash

	// Hash the record with Hash cleared, so the hash covers every other
	// field (PreviousHash included).
	unsigned := r
	unsigned.Hash = ""
	unsignedJSON, err := json.Marshal(unsigned)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(unsignedJSON)
	r.Hash = hex.EncodeToString(sum[:])

	final, err := json.Marshal(r)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(final, '\n')); err != nil {
		f.Close()
		return err
	}
	// fsync before releasing the flock (H7): the line must be durable on disk
	// before another process (or a crash) observes the chain tail, otherwise a
	// crash mid-append could leave a partial line that breaks VerifyChain.
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	w.lastHash = r.Hash

	// Best-effort chain link into kern (E-3): failures are logged to stderr
	// and never fail validation. Called while holding the mutex so chain
	// appends stay ordered the same way as the JSONL lines. The kern chain
	// hash (when kern returns one) is captured so a P1.4 receipt can
	// cross-link both chains.
	h, _ := w.linkToKernChain(r)
	w.lastKernChainHash = h
	return nil
}

// readLastHash scans the audit file for the last record's Hash, so a Writer
// opened on an existing JSONL continues the chain from where it left off — and
// so concurrent Writers chain onto the ACTUAL tail rather than a stale cache.
// A missing file, malformed last line, or record without a hash yields ""
// (genesis). Best-effort: chain integrity is verified separately by
// VerifyChain; this only feeds the next record's PreviousHash.
//
// The common path reads only the file's tail (the last record is always near
// the end), so the per-write cost stays constant as the chain grows; the full
// scan is the fallback for pathological files (a record larger than the tail
// window, or a tail window that cuts the final record with nothing parseable
// after it).
func (w *Writer) readLastHash() string {
	if h := w.readTailHash(); h != "" {
		return h
	}
	return w.readFullLastHash()
}

// readTailHash reads the last tailSize bytes of the audit file and returns
// the Hash of the last parseable, hash-carrying record in that window.
func (w *Writer) readTailHash() string {
	f, err := os.Open(w.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return ""
	}
	const tailSize = 256 * 1024
	start := fi.Size() - tailSize
	if start < 0 {
		start = 0
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return ""
	}
	// Walk the window's lines from the end: the last complete line is the
	// last record. A trailing partial line (crash mid-append) is skipped. The
	// window may begin mid-record — that first segment simply won't parse and
	// is ignored (we walk backwards from the end, so it is never reached
	// unless the whole window is garbage).
	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err == nil && rec.Hash != "" {
			return rec.Hash
		}
		// Unparseable or hash-less line: keep walking backwards (torn tail).
	}
	return ""
}

// readFullLastHash scans the entire audit file for the last record's Hash —
// the fallback when the tail window contains nothing parseable.
func (w *Writer) readFullLastHash() string {
	f, err := os.Open(w.path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	last := ""
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err == nil && rec.Hash != "" {
			last = rec.Hash
		}
	}
	return last
}

// LastHash returns the Hash of the most recently written record, or "" when
// nothing has been written yet. Used by the receipt generator (P1.4) to stamp
// a CI run's local audit-chain endpoint.
func (w *Writer) LastHash() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastHash
}

// LastKernChainHash returns the hash kern returned for the most recent chain
// link ("" when kern is unavailable, returned nothing parseable, or nothing
// has been written yet). P1.4 receipts cite it alongside the local chain hash
// as the cross-chain link.
func (w *Writer) LastKernChainHash() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastKernChainHash
}

// VerifyChain re-reads the entire audit JSONL and verifies the P1.4 hash
// chain: every record's stored Hash must equal sha256 over its canonical JSON
// (with Hash cleared but PreviousHash kept), and every record that carries a
// PreviousHash must have it equal to the preceding record's Hash. Records
// without a PreviousHash (the genesis record and legacy pre-P1.4 records) are
// treated as "" anchors: they self-verify but impose no linkage, so a mixed
// file verifies with the chain starting at the first record that carries the
// field.
//
// Genesis protection (H6): a record with empty PreviousHash is only legal
// BEFORE the chain has started — i.e. as a legacy prefix (records 0..N may
// have empty PreviousHash). Once a record with non-empty PreviousHash has
// been seen, every subsequent record must also carry a non-empty PreviousHash
// and chain correctly. An empty PreviousHash appearing after the chain has
// started is tampering (a forged anchor inserted mid-chain) and fails
// verification.
//
// Returns the last record's Hash on success, or an error naming the first
// broken link.
func (w *Writer) VerifyChain() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	data, err := os.ReadFile(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // no chain yet — trivially intact
		}
		return "", err
	}
	lastHash := ""
	chained := false // true once a record with non-empty PreviousHash is seen
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return "", fmt.Errorf("record %d: cannot parse: %v", i+1, err)
		}
		if rec.PreviousHash == "" {
			// Anchor (genesis or legacy prefix). Only legal before the chain
			// has started; after that an empty PreviousHash is a forged
			// anchor inserted mid-chain (H6).
			if chained {
				return "", fmt.Errorf("record %d (correlation_id %q): empty previous_hash after the chain has started (tampered head-prepend or mid-chain anchor)", i+1, rec.CorrelationID)
			}
		} else {
			if rec.PreviousHash != lastHash {
				return "", fmt.Errorf("record %d (correlation_id %q): previous_hash %q does not match preceding record hash %q", i+1, rec.CorrelationID, rec.PreviousHash, lastHash)
			}
			chained = true
		}
		unsigned := rec
		unsigned.Hash = ""
		b, err := json.Marshal(unsigned)
		if err != nil {
			return "", fmt.Errorf("record %d (correlation_id %q): cannot re-marshal for hash check: %v", i+1, rec.CorrelationID, err)
		}
		sum := sha256.Sum256(b)
		if want := hex.EncodeToString(sum[:]); rec.Hash != want {
			return "", fmt.Errorf("record %d (correlation_id %q): hash mismatch (record tampered): stored %q, recomputed %q", i+1, rec.CorrelationID, rec.Hash, want)
		}
		lastHash = rec.Hash
	}
	return lastHash, nil
}

// ChainContainsHash reports whether any record in the audit JSONL carries the
// given Hash (H3). It is used by `blueprint verify-receipt` to validate a
// receipt's audit_chain_hash against the chain as it stands TODAY: every CI
// run appends a record, so an earlier receipt's endpoint hash is no longer
// the chain's last hash — but it is still a genuine record hash somewhere in
// the chain, which is what proves the receipt was sealed at that point.
//
// An empty hash matches nothing (a receipt with no chain binding is rejected
// by the caller, exit 2). A missing file yields (false, nil) — there is no
// chain to contain the hash. A malformed line is an error, mirroring
// VerifyChain: an unreadable chain must not silently validate a receipt.
func (w *Writer) ChainContainsHash(hash string) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if hash == "" {
		return false, nil
	}
	data, err := os.ReadFile(w.path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return false, fmt.Errorf("record %d: cannot parse: %v", i+1, err)
		}
		if rec.Hash == hash {
			return true, nil
		}
	}
	return false, nil
}

// RecordCount returns the number of records (non-empty JSONL lines) in the
// audit file. Used by doctor and verify-receipt to report chain length.
func (w *Writer) RecordCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, err := os.ReadFile(w.path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// DistinctRepoRoots returns the unique, first-seen RepoRoot values carried by
// records in the audit file. `blueprint verify-receipt` uses this to discover
// the kern chain root(s) the records were linked under: `blueprint ci`
// validates in a throwaway worktree, so records' RepoRoot is the worktree
// path — and kern keys its audit chain by that exact path, not by the
// receipt's repo. Best-effort: a missing or unparseable file yields nil.
func (w *Writer) DistinctRepoRoots() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	data, err := os.ReadFile(w.path)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool)
	var roots []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if rec.RepoRoot != "" && !seen[rec.RepoRoot] {
			seen[rec.RepoRoot] = true
			roots = append(roots, rec.RepoRoot)
		}
	}
	return roots
}
