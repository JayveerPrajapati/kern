package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
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
func (s *TaskStore) Save(t Task) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked()
	if err != nil {
		return Task{}, err
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

// Get returns a task by ID, or os.ErrNotExist.
func (s *TaskStore) Get(id string) (Task, error) {
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