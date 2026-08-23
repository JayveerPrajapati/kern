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
		} else if it.Status == "final" {
			// Invariant 8: a finalized artifact is immutable. Block ANY
			// overwrite of an existing final artifact, regardless of the new
			// artifact's Status. Return the existing record rather than
			// replacing it.
			return it, fmt.Errorf("artifact %s is finalized and immutable", a.ID)
		}
	}
	kept = append(kept, a)
	if err := s.save(kept); err != nil {
		return domain.Artifact{}, err
	}
	return a, nil
}

// NewVersion writes the next version of an existing finalized artifact. It
// implements the Phase 3 "new version instead" rule: a finalized artifact is
// never silently mutated — when a successor must be produced, the existing
// final record is marked Status == "superseded" (kept intact for audit) and a
// new artifact with the same kind/task and Version+1 is written, linked to the
// superseded parent via ParentArtifactID. Draft/superseded artifacts are
// replaced freely (their status is not authoritative).
//
// It returns the new versioned artifact. If no existing artifact with the given
// ID is found, the provided artifact is saved as-is (treated as an initial
// version). If an existing artifact is already finalized, it is superseded and
// the new one gets Version = existing.Version + 1. If it is not finalized, the
// new artifact replaces it and inherits its version (no bump).
func (s *ArtifactStore) NewVersion(a domain.Artifact) (domain.Artifact, error) {
	if a.ID == "" {
		return domain.Artifact{}, fmt.Errorf("artifact: id is required for versioning")
	}
	list, err := s.load()
	if err != nil {
		return domain.Artifact{}, err
	}
	// Find the existing record with the same ID, if any.
	var existing *domain.Artifact
	for i := range list {
		if list[i].ID == a.ID {
			existing = &list[i]
			break
		}
	}
	if existing == nil {
		// Nothing to version against: persist as-is (initial version).
		return s.Save(a)
	}
	if existing.Status == "final" {
		// Supersede the finalized record (keep it for audit), write version+1.
		existing.Status = "superseded"
		a.Version = existing.Version + 1
		a.ParentArtifactID = existing.ID
	} else {
		// Not finalized: replace freely, preserving the existing version count.
		a.Version = existing.Version
	}
	// Rebuild the list with the superseded record replaced by the new version.
	kept := list[:0]
	for _, it := range list {
		if it.ID == a.ID {
			if existing.Status == "superseded" {
				kept = append(kept, *existing)
			}
			continue
		}
		kept = append(kept, it)
	}
	if existing.Status == "superseded" {
		// The superseded original was already appended; ensure ordering (orig first).
		// Re-sort deterministically: orig (superseded) before new version.
		kept = append(kept, a)
		sort.SliceStable(kept, func(i, j int) bool {
			if kept[i].ID == kept[j].ID {
				return kept[i].Status == "superseded"
			}
			return false
		})
	} else {
		kept = append(kept, a)
	}
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

// Replay reconstructs the artifact chain for a task, returning artifacts in
// chain order (following ParentArtifactID links from the root). This allows a
// complete analysis to be reconstructed from stored Task/Artifact/Evidence
// state without replaying the model (Strict Plan Phase 3 P2 + validation).
func (s *ArtifactStore) Replay(taskID string) ([]domain.Artifact, error) {
	all, err := s.GetByTask(taskID)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return []domain.Artifact{}, nil
	}
	// Build a map by ID for chain traversal.
	byID := make(map[string]domain.Artifact, len(all))
	for _, a := range all {
		byID[a.ID] = a
	}
	// Find the root (artifact with no ParentArtifactID within this task's set).
	var root domain.Artifact
	rootFound := false
	for _, a := range all {
		if a.ParentArtifactID == "" {
			root = a
			rootFound = true
			break
		}
		if _, ok := byID[a.ParentArtifactID]; !ok {
			root = a
			rootFound = true
			break
		}
	}
	if !rootFound {
		// No root found (circular or all linked); return sorted by time.
		return all, nil
	}
	// Follow the chain: root → child → child → ...
	chain := []domain.Artifact{root}
	visited := map[string]bool{root.ID: true}
	current := root
	for {
		nextID := ""
		for _, a := range all {
			if a.ParentArtifactID == current.ID && !visited[a.ID] {
				nextID = a.ID
				break
			}
		}
		if nextID == "" {
			break
		}
		chain = append(chain, byID[nextID])
		visited[nextID] = true
		current = byID[nextID]
	}
	return chain, nil
}

// ArtifactComparison describes the difference between two tasks' artifact
// chains.
type ArtifactComparison struct {
	TaskID1    string         `json:"task_id_1"`
	TaskID2    string         `json:"task_id_2"`
	OnlyIn1    []string       `json:"only_in_1"`    // artifact kinds present only in task 1
	OnlyIn2    []string       `json:"only_in_2"`    // artifact kinds present only in task 2
	InBoth     []string       `json:"in_both"`      // artifact kinds present in both
	DigestDiff map[string][2]string `json:"digest_diff"` // kind → [digest1, digest2] where they differ
}

// Compare compares the artifact chains of two tasks, reporting which artifact
// kinds are present in each and where digests differ (Strict Plan Phase 3 P2).
func (s *ArtifactStore) Compare(taskID1, taskID2 string) (*ArtifactComparison, error) {
	chain1, err := s.GetByTask(taskID1)
	if err != nil {
		return nil, err
	}
	chain2, err := s.GetByTask(taskID2)
	if err != nil {
		return nil, err
	}
	kinds1 := map[string]domain.Artifact{}
	for _, a := range chain1 {
		kinds1[string(a.Kind)] = a
	}
	kinds2 := map[string]domain.Artifact{}
	for _, a := range chain2 {
		kinds2[string(a.Kind)] = a
	}
	cmp := &ArtifactComparison{
		TaskID1:    taskID1,
		TaskID2:    taskID2,
		DigestDiff: map[string][2]string{},
	}
	for kind, a1 := range kinds1 {
		if a2, ok := kinds2[kind]; ok {
			cmp.InBoth = append(cmp.InBoth, kind)
			if a1.Digest != a2.Digest {
				cmp.DigestDiff[kind] = [2]string{a1.Digest, a2.Digest}
			}
		} else {
			cmp.OnlyIn1 = append(cmp.OnlyIn1, kind)
		}
	}
	for kind := range kinds2 {
		if _, ok := kinds1[kind]; !ok {
			cmp.OnlyIn2 = append(cmp.OnlyIn2, kind)
		}
	}
	sort.Strings(cmp.OnlyIn1)
	sort.Strings(cmp.OnlyIn2)
	sort.Strings(cmp.InBoth)
	return cmp, nil
}
