// Package approval implements the two-person approval state store (P1.3).
//
// Approval requests live in .blueprint/approvals/requests.jsonl as an
// append-only JSONL log, mirroring the audit trail's design: Create appends a
// pending request, Approve/Reject append a decision record, and Get/List
// read the log computing the current status as the LATEST record for an ID.
// The directory is created on first write.
package approval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Status is the lifecycle state of an approval request.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
	StatusExpired  Status = "expired"
)

// Request is one approval request. Each decision (Create/Approve/Reject)
// appends a full Request record; the record with the latest CreatedAt for an
// ID is authoritative.
type Request struct {
	ID        string     `json:"id"`
	RepoRoot  string     `json:"repo_root"`
	Intent    string     `json:"intent"`
	RiskLevel string     `json:"risk_level"`
	Requester string     `json:"requester"` // agent_id or "human"
	Files     []string   `json:"files,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	Status    Status     `json:"status"`
	Approver  string     `json:"approver,omitempty"`
	DecidedAt *time.Time `json:"decided_at,omitempty"`
	Reason    string     `json:"reason,omitempty"`
}

// Store is an append-only JSONL approval log rooted under .blueprint/approvals/.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a Store writing to
// <repoRoot>/.blueprint/approvals/requests.jsonl.
func NewStore(repoRoot string) *Store {
	return &Store{path: filepath.Join(repoRoot, ".blueprint", "approvals", "requests.jsonl")}
}

// Path returns the requests.jsonl path (useful for tests and diagnostics).
func (s *Store) Path() string { return s.path }

// Create appends a pending approval request. The ID must be non-empty and
// unique (the CLI generates it); the status is forced to pending so callers
// cannot inject a pre-approved request.
func (s *Store) Create(req Request) error {
	if strings.TrimSpace(req.ID) == "" {
		return fmt.Errorf("approval request: empty id")
	}
	req.Status = StatusPending
	return s.append(req)
}

// Get returns the current (latest) record for an ID, or an error when no
// record exists.
func (s *Store) Get(id string) (*Request, error) {
	recs, err := s.readAll()
	if err != nil {
		return nil, err
	}
	var latest *Request
	for i := range recs {
		if recs[i].ID == id {
			latest = &recs[i]
		}
	}
	if latest == nil {
		return nil, fmt.Errorf("approval request %q not found", id)
	}
	return latest, nil
}

// List returns the current record for every request, optionally filtered by
// status. A zero-value filter (empty string) returns all requests.
func (s *Store) List(filter Status) ([]Request, error) {
	recs, err := s.readAll()
	if err != nil {
		return nil, err
	}
	// Latest record per ID (last write wins).
	latest := make(map[string]Request)
	order := make([]string, 0, len(recs))
	for i := range recs {
		if _, seen := latest[recs[i].ID]; !seen {
			order = append(order, recs[i].ID)
		}
		latest[recs[i].ID] = recs[i]
	}
	out := make([]Request, 0, len(order))
	for _, id := range order {
		r := latest[id]
		if filter == "" || r.Status == filter {
			out = append(out, r)
		}
	}
	return out, nil
}

// Approve records a human approval decision. It errors when the request is
// unknown or already decided.
func (s *Store) Approve(id, approver, reason string) error {
	return s.decide(id, StatusApproved, approver, reason)
}

// Reject records a human rejection decision. It errors when the request is
// unknown or already decided.
func (s *Store) Reject(id, approver, reason string) error {
	return s.decide(id, StatusRejected, approver, reason)
}

// decide loads the current request and appends a decision record. The latest
// record wins, so the status transition is pending -> approved|rejected and
// any further decision on a decided request is an error.
func (s *Store) decide(id string, status Status, approver, reason string) error {
	cur, err := s.Get(id)
	if err != nil {
		return err
	}
	switch cur.Status {
	case StatusApproved, StatusRejected, StatusExpired:
		return fmt.Errorf("approval request %q already decided as %s", id, cur.Status)
	}
	if strings.TrimSpace(approver) == "" {
		return fmt.Errorf("approval request %q: approver identity required", id)
	}
	now := time.Now()
	cur.Status = status
	cur.Approver = approver
	cur.Reason = reason
	cur.DecidedAt = &now
	return s.append(*cur)
}

// append writes one JSONL record under a mutex so interleaved decisions stay
// ordered.
func (s *Store) append(req Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("approval store: create dir: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("approval store: open: %w", err)
	}
	defer f.Close()
	line, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("approval store: marshal: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("approval store: write: %w", err)
	}
	return nil
}

// readAll parses every record in the log. A missing file is an empty log.
func (s *Store) readAll() ([]Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("approval store: open: %w", err)
	}
	defer f.Close()
	var recs []Request
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		var r Request
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("approval store: %s:%d: %w", s.path, line, err)
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("approval store: read: %w", err)
	}
	return recs, nil
}
