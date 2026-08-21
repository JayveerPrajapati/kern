// File-backed persistence for the approval workflow.
package approval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/domain"
)

// FileStore is a file-backed approval store. It persists approvals as a single
// JSON file at <root>/.kern/approvals.json. It is safe for concurrent use.
//
// This exists so `kern approve` (CLI) can read pending approvals created by a
// running server or loop (which writes them to the same file), and approve them
// from a separate process.
type FileStore struct {
	mu   sync.RWMutex
	path string
}

// NewFileStore creates a FileStore at <root>/.kern/approvals.json. The directory
// is created if it does not exist.
func NewFileStore(root string) *FileStore {
	dir := filepath.Join(root, ".kern")
	_ = os.MkdirAll(dir, 0o755)
	return &FileStore{path: filepath.Join(dir, "approvals.json")}
}

// Load reads all approvals from the file. Returns an empty slice if the file
// does not exist.
func (s *FileStore) Load() ([]domain.Approval, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
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

// Save writes all approvals to the file atomically (temp file + rename).
func (s *FileStore) Save(approvals []domain.Approval) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(approvals, "", "  ")
	if err != nil {
		return fmt.Errorf("approval store: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("approval store: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("approval store: rename: %w", err)
	}
	return nil
}

// AddPending appends a pending approval and saves.
func (s *FileStore) AddPending(a domain.Approval) error {
	approvals, err := s.Load()
	if err != nil {
		return err
	}
	approvals = append(approvals, a)
	return s.Save(approvals)
}

// Decide marks an approval as approved or rejected, sets the DecidedAt timestamp,
// and saves. Returns the updated approval and an error if not found.
func (s *FileStore) Decide(approvalID, approver string, approved bool, reason string) (domain.Approval, error) {
	approvals, err := s.Load()
	if err != nil {
		return domain.Approval{}, err
	}
	for i, a := range approvals {
		if a.ID == approvalID {
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
			return approvals[i], s.Save(approvals)
		}
	}
	return domain.Approval{}, fmt.Errorf("approval not found: %s", approvalID)
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