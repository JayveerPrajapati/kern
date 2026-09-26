// File-backed persistence for the approval workflow.

package governance

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// maxResolvedApprovals bounds the number of decided (approved/rejected)
// approvals kept in the file. Pending approvals are never pruned; resolved
// history beyond this bound is dropped on the next save so the file cannot
// grow unboundedly while recent decisions stay auditable.
const maxResolvedApprovals = 100

// FileStore is a file-backed approval store. It persists approvals as a single
// JSON file at <root>/.kern/approvals.json. It is safe for concurrent use:
// a per-instance mutex serializes save/load within one instance, the
// process-wide per-path mutex (internal/cache/keylock) serializes multiple
// store instances in one process, and the cross-process flock
// (internal/cache/filelock) serializes separate kern processes (server, CLI,
// MCP) working on the same project so their read-modify-write critical
// sections never interleave and lose each other's updates.
// This exists so `kern approve` (CLI) can read pending approvals created by a
// running server or loop (which writes them to the same file), and approve them
// from a separate process.
type FileStore struct {
	mu   sync.RWMutex // serializes save/load against concurrent in-process writers
	path string
	// loadErr records a failure of the construction-time prime load (a missing
	// file is not an error). It is advisory: every operation re-reads the file
	// and fails closed on its own while it is unreadable, and recovers
	// automatically once the file is repaired.
	loadErr error
}

// NewFileStore creates a FileStore at <root>/.kern/approvals.json. The directory
// is created if it does not exist. The file is loaded on construction so
// approvals persisted by a previous process are visible immediately (restore
// on startup) and a corrupt store fails fast instead of being discovered on a
// later read.
func NewFileStore(root string) *FileStore {
	dir := filepath.Join(root, ".kern")
	// The approvals directory holds pending human-approval decisions; create
	// it owner-only (0o700) so other local users cannot read who approved
	// what or the gated task keys (audit A2: approvals are sensitive).
	_ = os.MkdirAll(dir, 0o700)
	s := &FileStore{path: filepath.Join(dir, "approvals.json")}
	// Prime the store on construction. Every read/write re-loads the file (so
	// the store observes cross-process writes), but loading here surfaces a
	// corrupt file early and satisfies restore-on-startup for read-only
	// callers. A missing file is not an error.
	if _, err := s.loadLocked(); err != nil {
		// Fail loudly instead of silently treating the store as empty: a
		// corrupt file that is later saved over would destroy the pending
		// approvals it contains. Every operation re-reads the file and fails
		// closed with the same error until the file is repaired.
		s.loadErr = err
		log.Printf("kern governance: approval store %s failed to load: %v (reads and writes will fail until it is repaired)", s.path, err)
	}
	return s
}

// LoadError returns the error from the construction-time prime load, if any.
// A missing file is not an error. Callers can use it to fail fast on a corrupt
// store instead of discovering the failure on the first read/write.
func (s *FileStore) LoadError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadErr
}

// loadLocked is the lock-free read core. Caller must hold s.mu (read or write).
func (s *FileStore) loadLocked() ([]domain.Approval, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("approval store: read %s: %w", s.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var approvals []domain.Approval
	if err := json.Unmarshal(data, &approvals); err != nil {
		return nil, fmt.Errorf("approval store: unmarshal: %w", err)
	}
	return approvals, nil
}

// saveLocked is the lock-free write core. Caller must hold s.mu (write).
// It writes to a unique temp file (os.CreateTemp) then renames atomically,
// avoiding cross-process temp-file collisions.
func (s *FileStore) saveLocked(approvals []domain.Approval) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(approvals, "", "  ")
	if err != nil {
		return fmt.Errorf("approval store: marshal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".approvals-tmp-*")
	if err != nil {
		return fmt.Errorf("approval store: create temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("approval store: write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("approval store: close %s: %w", tmp.Name(), err)
	}
	// Owner-only (0o600): the approvals file carries pending decisions and
	// task keys that other local users must not read (audit A2).
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("approval store: chmod %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("approval store: rename: %w", err)
	}
	return nil
}

// Load reads all approvals from the file. Returns nil if the file does not
// exist.
func (s *FileStore) Load() ([]domain.Approval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked()
}

// Save writes all approvals to the file atomically (unique temp file + rename),
// under the process-wide path lock and the cross-process flock. Resolved
// approvals are pruned to a bounded history on save.
func (s *FileStore) Save(approvals []domain.Approval) error {
	return s.mutate(func([]domain.Approval) []domain.Approval { return approvals })
}

// AddPending inserts (or replaces, by ID) an approval and saves. Upserting by
// ID keeps concurrent writers — the workflow's own persisted backend and the
// engine's separate store instance on the same file — from duplicating an
// approval.
func (s *FileStore) AddPending(a domain.Approval) error {
	return s.mutate(func(approvals []domain.Approval) []domain.Approval {
		kept := approvals[:0]
		for _, it := range approvals {
			if it.ID != a.ID {
				kept = append(kept, it)
			}
		}
		return append(kept, a)
	})
}

// Consume atomically removes the approval with the given ID and reports
// whether it was present (claimed). The removal runs inside the single
// flock'd load-modify-save critical section (mutate), so two concurrent
// claims for the same ID can never both report true — the single-use
// guarantee for exec approvals (gate-1 attempt-2: a check-then-consume
// outside the lock let two concurrent identical calls both pass).
func (s *FileStore) Consume(approvalID string) (bool, error) {
	claimed := false
	err := s.mutate(func(approvals []domain.Approval) []domain.Approval {
		kept := approvals[:0]
		for _, it := range approvals {
			if it.ID == approvalID {
				claimed = true
				continue
			}
			kept = append(kept, it)
		}
		return kept
	})
	if err != nil {
		return false, err
	}
	return claimed, nil
}

// Decide marks an approval as approved or rejected, sets the DecidedAt timestamp,
// and saves. Returns the updated approval and an error if not found.
func (s *FileStore) Decide(approvalID, approver string, approved bool, reason string) (domain.Approval, error) {
	var decided domain.Approval
	// R1: a decision that changes an exec-stamped record's Status invalidates
	// its HMAC (the MAC covers Status), so the record must be re-stamped. If
	// the secret is unavailable the decision fails closed — an unverifiable
	// record can never grant execution.
	var stampErr error
	var integrityErr error
	err := s.mutate(func(approvals []domain.Approval) []domain.Approval {
		for i := range approvals {
			if approvals[i].ID == approvalID {
				// Gate-1 attempt-2: refuse to decide an exec-stamped record
				// whose integrity does not verify. Deciding would re-stamp
				// and launder the tamper into a fully-valid grant, so a
				// tampered pending record must never be decidable.
				if mac := execApprovalStampFrom(approvals[i]); mac != "" && !execApprovalMACValid(approvals[i]) {
					integrityErr = fmt.Errorf("approval %s: integrity check failed (record was modified outside the approval workflow); refusing to decide", approvalID)
					return approvals
				}
				now := time.Now()
				approvals[i].Approver = approver
				approvals[i].DecidedAt = &now
				if reason != "" {
					approvals[i].Reason = reason
				}
				if approved {
					approvals[i].Status = "approved"
				} else {
					approvals[i].Status = "rejected"
				}
				// Exec-path integrity only: records carrying the exec HMAC
				// marker are re-stamped over their new status; records from
				// other features sharing this store are untouched.
				if mac := execApprovalStampFrom(approvals[i]); mac != "" {
					if re, rerr := execApprovalStamp(approvals[i]); rerr != nil {
						stampErr = rerr
					} else {
						approvals[i].ArtifactID = re
					}
				}
				decided = approvals[i]
				break
			}
		}
		return approvals
	})
	if err != nil {
		return domain.Approval{}, err
	}
	if integrityErr != nil {
		return domain.Approval{}, integrityErr
	}
	if stampErr != nil {
		// The status change was persisted but the record's HMAC is now stale
		// (unverifiable), so the decision must not be reported as usable.
		return domain.Approval{}, fmt.Errorf("approval %s: decision recorded but integrity re-stamp failed (approval is unverifiable; failing closed): %w", approvalID, stampErr)
	}
	if decided.ID == "" {
		return domain.Approval{}, fmt.Errorf("approval not found: %s", approvalID)
	}
	// The decision is a governance-relevant event: record it in the project's
	// tamper-evident audit chain (the same store `kern audit` reads) so an
	// approval's approve/reject, agent, approval ID, and gated task are
	// reconstructable end-to-end. Best-effort: a failed audit write must not
	// fail the decision (the decision is persisted in approvals.json; a
	// missing chain entry is detectable via `kern audit repair`).
	s.recordAudit(decided, approver, approved, reason)
	return decided, nil
}

// recordAudit appends an approval decision to the project's shared audit
// chain. The entry carries the decision, the approver, the approval ID, and
// the gated task so `kern audit` / `kern audit <task-id>` surface it. The
// write is best-effort and never fails the caller.
func (s *FileStore) recordAudit(a domain.Approval, approver string, approved bool, reason string) {
	action := "reject"
	result := "denied"
	if approved {
		action = "approve"
		result = "approved"
	}
	if reason == "" {
		reason = fmt.Sprintf("%s by %s", result, approver)
	}
	entry := AuditEntry{
		AgentID:   approver,
		TaskID:    a.TaskID,
		Action:    action,
		Resource:  "approval:" + a.ID,
		Approved:  approved,
		Result:    result,
		Policy:    "approval",
		Reason:    reason,
		Timestamp: time.Now(),
	}
	if err := s.auditLog().AppendExternal(entry); err != nil {
		// Loud, non-blocking: the decision already happened and must not be
		// rolled back because the audit trail could not be written (a missing
		// chain entry is detectable via chain repair; a half-written approval
		// is not).
		log.Printf("kern governance: approval %s %s by %s NOT recorded in audit chain: %v", a.ID, result, approver, err)
	}
}

// auditLog returns the project's shared tamper-evident audit log, rebuilt per
// call so every store instance observes the true persisted chain head.
func (s *FileStore) auditLog() *AuditLog {
	auditDir := filepath.Join(filepath.Dir(filepath.Dir(s.path)), ".kern", "audit")
	return NewAuditLog().
		WithStore(storage.NewLog(auditDir)).
		WithLockPath(filepath.Join(auditDir, ".lock"))
}

// Pending returns only approvals with Status "pending", sorted by RequestedAt.
func (s *FileStore) Pending() ([]domain.Approval, error) {
	approvals, err := s.Load()
	if err != nil {
		return nil, err
	}
	var pending []domain.Approval
	for _, a := range approvals {
		if a.Status == "pending" || a.Status == "" {
			pending = append(pending, a)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].RequestedAt.Before(pending[j].RequestedAt)
	})
	return pending, nil
}

// Decisions returns only the decided (approved/rejected) approvals, ordered
// deterministically by decision time (falling back to request time) then ID,
// so callers such as the policy-signal learner see a stable history. Pending
// approvals are excluded.
func (s *FileStore) Decisions() ([]domain.Approval, error) {
	approvals, err := s.Load()
	if err != nil {
		return nil, err
	}
	var decided []domain.Approval
	for _, a := range approvals {
		if a.Status == "approved" || a.Status == "rejected" {
			decided = append(decided, a)
		}
	}
	sort.Slice(decided, func(i, j int) bool {
		if !resolvedAt(decided[i]).Equal(resolvedAt(decided[j])) {
			return resolvedAt(decided[i]).Before(resolvedAt(decided[j]))
		}
		return decided[i].ID < decided[j].ID
	})
	return decided, nil
}

// Get returns a single approval by ID.
func (s *FileStore) Get(approvalID string) (domain.Approval, error) {
	approvals, err := s.Load()
	if err != nil {
		return domain.Approval{}, err
	}
	for _, a := range approvals {
		if a.ID == approvalID {
			return a, nil
		}
	}
	return domain.Approval{}, fmt.Errorf("approval not found: %s", approvalID)
}

// mutate runs a load->modify->save critical section on the backing file under
// the process-wide per-path mutex and the cross-process file lock, so separate
// store instances (web app, MCP server, CLI) and separate kern processes never
// interleave their read-modify-write and lose each other's updates. Resolved
// approvals are pruned to a bounded history on every save.
func (s *FileStore) mutate(fn func([]domain.Approval) []domain.Approval) error {
	fl, err := cache.LockFile(s.path)
	if err != nil {
		return fmt.Errorf("approval store: lock %s: %w", s.path, err)
	}
	defer fl.Unlock()
	pl := cache.PathLock(s.path)
	pl.Lock()
	defer pl.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	approvals, err := s.loadLocked()
	if err != nil {
		return err
	}
	return s.saveLocked(pruneApprovals(fn(approvals)))
}

// pruneApprovals keeps every pending approval and the newest
// maxResolvedApprovals resolved (approved/rejected) approvals, so the file
// stays bounded while recent decisions remain queryable.
func pruneApprovals(approvals []domain.Approval) []domain.Approval {
	var pending []domain.Approval
	var resolved []domain.Approval
	for _, a := range approvals {
		if a.Status == "" || a.Status == "pending" {
			pending = append(pending, a)
			continue
		}
		resolved = append(resolved, a)
	}
	if len(resolved) <= maxResolvedApprovals {
		return append(pending, resolved...)
	}
	sort.Slice(resolved, func(i, j int) bool {
		return resolvedAt(resolved[i]).After(resolvedAt(resolved[j]))
	})
	return append(pending, resolved[:maxResolvedApprovals]...)
}

// resolvedAt returns the decision time of a resolved approval, falling back to
// the request time when no decision was recorded.
func resolvedAt(a domain.Approval) time.Time {
	if a.DecidedAt != nil {
		return *a.DecidedAt
	}
	return a.RequestedAt
}
