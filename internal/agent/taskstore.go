package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// TaskStore is a JSON file store for tasks under the project cache.
type TaskStore struct {
	mu   sync.Mutex // serializes save/load against concurrent writers
	root string
	path string
}

// NewTaskStore returns a task store rooted at the given project root.
// Storage path: <cache_dir>/tasks/<project_hash>.json
func NewTaskStore(root string) *TaskStore {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	path := cache.Path("tasks", cache.Hash([]byte(abs))+".json")
	return &TaskStore{root: root, path: path}
}

// load reads the persisted tasks, returning an empty slice if the file is
// absent. A corrupt file surfaces its unmarshal error instead of being
// silently treated as an empty store.
func (s *TaskStore) load() ([]Task, error) {
	return s.loadLocked()
}

// loadLocked is the unlocked inner reader; callers must hold s.mu.
func (s *TaskStore) loadLocked() ([]Task, error) {
	var list []Task
	b, err := os.ReadFile(s.path)
	if err != nil {
		return []Task{}, nil
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Task{}
	}
	return list, nil
}

// save writes the task list atomically using a unique temp file per write so
// concurrent saves never collide on the same path.
func (s *TaskStore) save(list []Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(list)
}

// saveLocked is the unlocked inner writer; callers must hold s.mu.
func (s *TaskStore) saveLocked(list []Task) error {
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

// Save persists a task (insert or replace by ID) and returns the stored record.
// It takes the process-wide path lock so concurrent store instances (MCP
// server, web app, CLI) serialize their read-modify-write and never lose each
// other's updates, and a cross-process file lock so separate kern processes
// working on the same project (or parallel test binaries) do not interleave
// their load->modify->save critical sections on the shared JSON file.
// When t.ID is empty, the store assigns the next deterministic ID
// ("t-<max+1>") from the persisted content under the lock. This makes task
// IDs unique across processes: the old package-level counter started at t-1 in
// every process, so two processes created colliding IDs and Save (replace by
// ID) silently destroyed one of the tasks. Interfaces that want the store to
// own IDs (TaskService, web registry) submit tasks with an empty ID.
func (s *TaskStore) Save(t Task) (Task, error) {
	fl, err := cache.LockFile(s.path)
	if err != nil {
		return Task{}, err
	}
	defer fl.Unlock()
	pl := cache.PathLock(s.path)
	pl.Lock()
	defer pl.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return Task{}, err
	}
	if t.ID == "" {
		t.ID = nextStoreTaskID(list)
	}
	kept := list[:0]
	for _, it := range list {
		if it.ID != t.ID {
			kept = append(kept, it)
		}
	}
	kept = append(kept, t)
	if err := s.saveLocked(kept); err != nil {
		return Task{}, err
	}
	return t, nil
}

// nextStoreTaskID returns the next deterministic task ID from the persisted
// list: the highest numeric "t-<n>" suffix plus one (t-1 when the store is
// empty or holds no t-<n> IDs). Non-numeric IDs (e.g. "t-replay" in tests)
// are ignored so they never force an ID collision.
func nextStoreTaskID(list []Task) string {
	maxN := 0
	for _, it := range list {
		if n, ok := parseTaskNumber(it.ID); ok && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("t-%d", maxN+1)
}

// parseTaskNumber extracts the numeric suffix of a "t-<n>" ID.
func parseTaskNumber(id string) (int, bool) {
	if len(id) < 3 || id[:2] != "t-" {
		return 0, false
	}
	n, err := strconv.Atoi(id[2:])
	if err != nil {
		return 0, false
	}
	return n, true
}

// Get returns a task by ID, or os.ErrNotExist. The path lock guards against
// reading a half-renamed file while another instance is mid-save.
func (s *TaskStore) Get(id string) (Task, error) {
	pl := cache.PathLock(s.path)
	pl.Lock()
	defer pl.Unlock()
	list, err := s.load()
	if err != nil {
		return Task{}, err
	}
	for _, it := range list {
		if it.ID == id {
			return it, nil
		}
	}
	return Task{}, os.ErrNotExist
}

// List returns all persisted tasks.
func (s *TaskStore) List() ([]Task, error) {
	return s.load()
}
