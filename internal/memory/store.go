// Package memory persists typed engineering knowledge per project. It extends
// the legacy lesson-only API (memory.go) with a store supporting every
// domain.MemoryType, saved to a separate JSON file so v1 behavior is unchanged.
package memory

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// maxTypedEntries caps the number of typed memories persisted per store. When
// the cap is exceeded the oldest entries are dropped (FIFO eviction) so the
// store cannot grow without bound.
const maxTypedEntries = 200

// MemoryStore is a typed engineering memory store supporting all MemoryType
// values. It persists to a separate JSON file from the legacy lesson store
// (memory.json -> ememory.json) so v1 behavior is untouched.
//
// mu serializes the load→mutate→save cycle so concurrent writers cannot clobber
// each other's updates.
type MemoryStore struct {
	root string
	path string
	mu   sync.Mutex
}

// NewStore returns a typed memory store for the given project root.
// Storage path: <cache_dir>/ememory/<project_hash>.json (separate from v1).
func NewMemoryStore(root string) *MemoryStore {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	path := cache.Path("ememory", cache.Hash([]byte(abs))+".json")
	return &MemoryStore{root: root, path: path}
}

// load reads the typed store from disk, returning an empty store if absent. If
// the file exists but is corrupt JSON, it is renamed to "<path>.corrupt" so the
// data is preserved for recovery and never silently overwritten on the next
// save; a fresh empty store is returned.
func (s *MemoryStore) load() []domain.Memory {
	var ms []domain.Memory
	b, err := os.ReadFile(s.path)
	if err != nil {
		return []domain.Memory{}
	}
	if err := json.Unmarshal(b, &ms); err != nil {
		if re := os.Rename(s.path, s.path+".corrupt"); re != nil {
			log.Printf("memory: corrupt store %s renamed to %s.corrupt: %v (rename: %v)", s.path, s.path, err, re)
		} else {
			log.Printf("memory: corrupt store %s renamed to %s.corrupt: %v", s.path, s.path, err)
		}
		return []domain.Memory{}
	}
	if ms == nil {
		ms = []domain.Memory{}
	}
	return ms
}

func (s *MemoryStore) save(ms []domain.Memory) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Add stores a new typed memory entry. Returns the entry with ID and
// timestamps set.
func (s *MemoryStore) Add(m domain.Memory) (domain.Memory, error) {
	if strings.TrimSpace(m.Content) == "" {
		return m, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.load()
	if m.ID == "" {
		m.ID = newID(m.Content, ms)
	}
	now := time.Now().UTC()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	ms = append(ms, m)
	// FIFO eviction: keep only the newest maxTypedEntries, dropping the oldest.
	if len(ms) > maxTypedEntries {
		ms = ms[len(ms)-maxTypedEntries:]
	}
	if err := s.save(ms); err != nil {
		return domain.Memory{}, err
	}
	return m, nil
}

// Supersede marks an older memory with the same type+scope as superseded and
// promotes a new one to current (Phase 15.4 memory supersession). This makes
// the newest memory authoritative while retaining the older one for audit.
// The new memory may be an existing entry (promote) or a fresh one (replace).
func (s *MemoryStore) Supersede(newMemory domain.Memory) (domain.Memory, error) {
	if strings.TrimSpace(newMemory.Content) == "" {
		return domain.Memory{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.load()
	now := time.Now().UTC()

	// Find all memories of the same type+scope that are not already superseded.
	var out []domain.Memory
	for _, m := range ms {
		if m.Type == newMemory.Type && m.Scope == newMemory.Scope && m.ID != newMemory.ID {
			m.Status = domain.MemorySuperseded
		}
		out = append(out, m)
	}

	newMemory.Status = domain.MemoryCurrent
	if newMemory.ID == "" {
		newMemory.ID = newID(newMemory.Content, ms)
	}
	if newMemory.CreatedAt.IsZero() {
		newMemory.CreatedAt = now
	}
	newMemory.UpdatedAt = now
	out = append(out, newMemory)
	if len(out) > maxTypedEntries {
		out = out[len(out)-maxTypedEntries:]
	}
	if err := s.save(out); err != nil {
		return domain.Memory{}, err
	}
	return newMemory, nil
}

// CurrentMemories returns only the memories that are current (not superseded
// and not historical) for the given type (Phase 15.4). An empty type returns
// all current memories.
func (s *MemoryStore) CurrentMemories(memType domain.MemoryType) ([]domain.Memory, error) {
	ms := s.load()
	out := make([]domain.Memory, 0, len(ms))
	for _, m := range ms {
		if m.Status == domain.MemorySuperseded || m.Status == domain.MemoryHistorical {
			continue
		}
		if memType != "" && m.Type != memType {
			continue
		}
		out = append(out, m)
	}
	sortRecency(out)
	return out, nil
}

// MarkHistorical retires a memory to the historical state (Phase 15.4),
// removing it from the authoritative set without deleting it.
func (s *MemoryStore) MarkHistorical(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.load()
	updated := false
	for i := range ms {
		if ms[i].ID == id {
			ms[i].Status = domain.MemoryHistorical
			updated = true
			break
		}
	}
	if !updated {
		return os.ErrNotExist
	}
	return s.save(ms)
}

// List returns all memories, optionally filtered by type. If memType is empty,
// returns all types. Results are sorted by CreatedAt descending.
func (s *MemoryStore) List(memType domain.MemoryType) ([]domain.Memory, error) {
	ms := s.load()
	out := make([]domain.Memory, 0, len(ms))
	for _, m := range ms {
		if memType != "" && m.Type != memType {
			continue
		}
		out = append(out, m)
	}
	sortRecency(out)
	return out, nil
}

// Get retrieves a memory by ID.
func (s *MemoryStore) Get(id string) (domain.Memory, error) {
	for _, m := range s.load() {
		if m.ID == id {
			return m, nil
		}
	}
	return domain.Memory{}, os.ErrNotExist
}

// Delete removes a memory by ID.
func (s *MemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.load()
	kept := ms[:0]
	for _, m := range ms {
		if m.ID != id {
			kept = append(kept, m)
		}
	}
	if len(kept) == len(ms) {
		return os.ErrNotExist
	}
	return s.save(kept)
}

// Update modifies an existing memory's content/tags.
func (s *MemoryStore) Update(id string, content string, tags []string) (domain.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := s.load()
	for i := range ms {
		if ms[i].ID != id {
			continue
		}
		if content != "" {
			ms[i].Content = content
		}
		if tags != nil {
			ms[i].Tags = tags
		}
		ms[i].UpdatedAt = time.Now().UTC()
		if err := s.save(ms); err != nil {
			return domain.Memory{}, err
		}
		return ms[i], nil
	}
	return domain.Memory{}, os.ErrNotExist
}

// newID returns a short, collision-resistant, content-derived identifier.
// It suffixes the content hash with an occurrence count so duplicates get
// distinct IDs.
func newID(content string, ms []domain.Memory) string {
	base := cache.Hash([]byte(content))[:12]
	n := 0
	for _, m := range ms {
		if strings.HasPrefix(m.ID, base) {
			n++
		}
	}
	return base + "-" + itoa(n)
}

// itoa is a minimal integer-to-string helper (avoids strconv dependency).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// AuthorizedRecall recalls memories matching the query, filtered by the
// caller's security clearance. Memories with a Classification higher than the
// caller's clearance are excluded. This closes spec §41 F-55 (per-agent
// authorization for shared memory).
//
// clearanceLevels maps classification strings to numeric levels:
//
//	"" (unclassified) = 0, "public" = 0, "internal" = 1,
//	"confidential" = 2, "restricted" = 3
//
// A caller with clearance N can read memories with classification <= N.
// agentID is reserved for future per-agent attribution/auditing.
func (s *MemoryStore) AuthorizedRecall(q Query, agentID string, clearance int) ([]domain.Memory, error) {
	mems, err := s.Recall(q)
	if err != nil {
		return nil, err
	}
	filtered := mems[:0]
	for _, m := range mems {
		if classificationLevel(m.Classification) <= clearance {
			filtered = append(filtered, m)
		}
	}
	return filtered, nil
}

// classificationLevel maps a classification string to a numeric level.
func classificationLevel(c string) int {
	switch c {
	case domain.ClassificationRestricted:
		return 3
	case domain.ClassificationConfidential:
		return 2
	case domain.ClassificationInternal:
		return 1
	default: // "" or "public" or unknown
		return 0
	}
}

// sortRecency sorts memories by CreatedAt descending (most recent first).
func sortRecency(ms []domain.Memory) {
	sort.SliceStable(ms, func(i, j int) bool {
		return ms[i].CreatedAt.After(ms[j].CreatedAt)
	})
}
