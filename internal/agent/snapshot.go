package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Snapshot is a point-in-time copy of a task's state, recorded on every
// persist. It supports audit, debugging, and resume-after-restart by
// preserving the full state history of each task.
type Snapshot struct {
	TaskID    string           `json:"task_id"`
	State     domain.TaskState `json:"state"`
	AgentID   string           `json:"agent_id,omitempty"`
	Output    string           `json:"output,omitempty"`
	Timestamp time.Time        `json:"timestamp"`

	// Rich snapshot fields (Phase 1.7). These carry the compact resume/replay
	// context so a snapshot matches the domain.ContextSnapshot JSON shape. They
	// are additive; the fields above are unchanged for backward compatibility.
	Goal        string   `json:"goal"`
	Decisions   []string `json:"decisions"`
	Constraints []string `json:"constraints"`
	Files       []string `json:"files"`
	Tests       []string `json:"tests"`
	Risks       []string `json:"risks"`
	NextAction  string   `json:"next_action"`
}

// SnapshotStore is a JSON file store for task snapshots. Snapshots are
// appended per task, forming a history index.
type SnapshotStore struct {
	mu   sync.Mutex
	root string
	path string // <cache_dir>/snapshots/<project_hash>.json
}

// NewSnapshotStore returns a snapshot store rooted at the given project root.
func NewSnapshotStore(root string) *SnapshotStore {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	path := cache.Path("snapshots", cache.Hash([]byte(abs))+".json")
	return &SnapshotStore{root: root, path: path}
}

// Record appends a snapshot for the given task. Snapshots are stored
// chronologically per task.
func (s *SnapshotStore) Record(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return err
	}
	snap := Snapshot{
		TaskID:    t.ID,
		State:     t.State,
		AgentID:   t.AgentID,
		Output:    t.Output,
		Timestamp: time.Now().UTC(),
	}
	cs := t.Snapshot()
	snap.Goal = cs.Goal
	snap.Decisions = cs.Decisions
	snap.Constraints = cs.Constraints
	snap.Files = cs.Files
	snap.Tests = cs.Tests
	snap.Risks = cs.Risks
	snap.NextAction = cs.NextAction
	all = append(all, snap)
	return s.saveLocked(all)
}

// History returns all snapshots for a task, oldest first.
func (s *SnapshotStore) History(taskID string) ([]Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	var result []Snapshot
	for _, snap := range all {
		if snap.TaskID == taskID {
			result = append(result, snap)
		}
	}
	return result, nil
}

// ListByState returns snapshot IDs for tasks currently in the given state
// (based on their most recent snapshot). This is a history index: it answers
// "which tasks are in state X right now" without loading full task records.
func (s *SnapshotStore) ListByState(state domain.TaskState) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	// Build a map of taskID → most recent snapshot state.
	latest := map[string]domain.TaskState{}
	for _, snap := range all {
		latest[snap.TaskID] = snap.State // later entries overwrite earlier
	}
	var ids []string
	for id, st := range latest {
		if st == state {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// ListSince returns task IDs that have at least one snapshot since the given
// time. This is a history index: it answers "which tasks changed since T".
func (s *SnapshotStore) ListSince(since time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, snap := range all {
		if !snap.Timestamp.Before(since) && !seen[snap.TaskID] {
			seen[snap.TaskID] = true
			ids = append(ids, snap.TaskID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// loadLocked reads the persisted snapshots. Returns an empty slice if the file
// is absent. A corrupt file surfaces its unmarshal error.
func (s *SnapshotStore) loadLocked() ([]Snapshot, error) {
	var list []Snapshot
	b, err := os.ReadFile(s.path)
	if err != nil {
		return []Snapshot{}, nil
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Snapshot{}
	}
	return list, nil
}

// saveLocked writes the snapshot list atomically.
func (s *SnapshotStore) saveLocked(list []Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), "*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, s.path)
}
