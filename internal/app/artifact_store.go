package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// ArtifactStore is a JSON file store for domain.Artifact records, backing the
// Phase 3 unified artifact chain. It persists artifacts to
// <cache_dir>/artifacts/<project_hash>.json so the chain survives restarts
// and is queryable via GET /v1/artifacts/{id} and `kern artifacts <task-id>`.
//
// Artifacts are keyed by ID (insert-or-replace by ID). The store is safe for
// concurrent use: a mutex serializes save/load against concurrent writers.
type ArtifactStore struct {
	mu   sync.Mutex
	root string
	path string
}

// NewArtifactStore returns an artifact store rooted at the given project root.
func NewArtifactStore(root string) *ArtifactStore {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	path := cache.Path("artifacts", cache.Hash([]byte(abs))+".json")
	return &ArtifactStore{root: root, path: path}
}

// load reads the persisted artifacts, returning an empty slice if the file is
// absent. A corrupt file surfaces its unmarshal error.
func (s *ArtifactStore) load() ([]domain.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *ArtifactStore) loadLocked() ([]domain.Artifact, error) {
	var list []domain.Artifact
	b, err := os.ReadFile(s.path)
	if err != nil {
		return []domain.Artifact{}, nil
	}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []domain.Artifact{}
	}
	return list, nil
}

// save writes the artifact list atomically.
func (s *ArtifactStore) save(list []domain.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(list)
}

func (s *ArtifactStore) saveLocked(list []domain.Artifact) error {
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

// Save persists an artifact (insert or replace by ID) and returns the stored
// record. It is the canonical way to record a typed artifact in the workflow
// chain. The caller is responsible for setting ParentArtifactID to link the
// artifact into the chain (TaskService does this automatically).
//
// Invariant 8: finalized artifacts (Status == "final") are immutable — a Save
// that would overwrite an existing final artifact returns an error instead of
// replacing it. Draft ("draft") and superseded ("superseded") artifacts may be
// replaced freely.
func (s *ArtifactStore) Save(a domain.Artifact) (domain.Artifact, error) {
	list, err := s.load()
	if err != nil {
		return domain.Artifact{}, err
	}
	kept := list[:0]
	for _, it := range list {
		if it.ID != a.ID {
			kept = append(kept, it)
		} else if it.Status == "final" && a.Status == "final" {
			// Invariant 8: a finalized artifact is immutable. Return the
			// existing record rather than overwriting it.
			return it, fmt.Errorf("artifact %s is finalized and immutable", a.ID)
		}
	}
	kept = append(kept, a)
	if err := s.save(kept); err != nil {
		return domain.Artifact{}, err
	}
	return a, nil
}

// Get returns an artifact by ID, or an error wrapping os.ErrNotExist.
func (s *ArtifactStore) Get(id string) (domain.Artifact, error) {
	list, err := s.load()
	if err != nil {
		return domain.Artifact{}, err
	}
	for _, it := range list {
		if it.ID == id {
			return it, nil
		}
	}
	return domain.Artifact{}, fmt.Errorf("%w: artifact %q", os.ErrNotExist, id)
}

// GetByTask returns all artifacts for a given task ID, sorted by CreatedAt.
func (s *ArtifactStore) GetByTask(taskID string) ([]domain.Artifact, error) {
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []domain.Artifact
	for _, it := range list {
		if it.TaskID == taskID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// List returns all persisted artifacts, sorted by CreatedAt.
func (s *ArtifactStore) List() ([]domain.Artifact, error) {
	list, err := s.load()
	if err != nil {
		return nil, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	return list, nil
}
