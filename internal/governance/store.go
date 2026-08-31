// File-backed persistence for the approval workflow.
package governance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
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
}

// NewFileStore creates a FileStore at <root>/.kern/approvals.json. The directory
// is created if it does not exist. The file is loaded on construction so
// approvals persisted by a previous process are visible immediately (restore
// on startup) and a corrupt store fails fast instead of being discovered on a
// later read.
func NewFileStore(root string) *FileStore {
	dir := filepath.Join(root, ".kern")
	_ = os.MkdirAll(dir, 0o755)
	s := &FileStore{path: filepath.Join(dir, "approvals.json")}
	// Prime the store on construction. Every read/write re-loads the file (so
	// the store observes cross-process writes), but loading here surfaces a
	// corrupt file early and satisfies restore-on-startup for read-only
	// callers. A missing file is not an error.
	_, _ = s.loadLocked()
	return s
}

// loadLocked is the lock-free read core. Caller must hold s.mu (read or write).
func (s *FileStore) loadLocked() ([]domain.Approval, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
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
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
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
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("approval store: write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("approval store: close %s: %w", tmp.Name(), err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("approval store: chmod %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
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

// Decide marks an approval as approved or rejected, sets the DecidedAt timestamp,
// and saves. Returns the updated approval and an error if not found.
func (s *FileStore) Decide(approvalID, approver string, approved bool, reason string) (domain.Approval, error) {
	var decided domain.Approval
	err := s.mutate(func(approvals []domain.Approval) []domain.Approval {
		for i := range approvals {
			if approvals[i].ID == approvalID {
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
				decided = approvals[i]
				break
			}
		}
		return approvals
	})
	if err != nil {
		return domain.Approval{}, err
	}
	if decided.ID == "" {
		return domain.Approval{}, fmt.Errorf("approval not found: %s", approvalID)
	}
	return decided, nil
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
