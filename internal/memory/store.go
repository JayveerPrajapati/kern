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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// maxTypedEntries caps the number of typed memories persisted per store. When
// the cap is exceeded the oldest entries are dropped (FIFO eviction) so the
// store cannot grow without bound.
const maxTypedEntries = 200

// MemoryStore is a typed engineering memory store supporting all MemoryType
// values. It persists to a separate JSON file from the legacy lesson store
// (memory.json -> ememory.json) so v1 behavior is untouched.
// mu serializes the load→mutate→save cycle so concurrent writers cannot clobber
// each other's updates.
//
// gov is the optional governance layer (access control, audit trail,
// retention). It is nil for legacy stores created with NewMemoryStore and is
// attached with WithGovernance; it is guarded by govMu so it can be read
// while s.mu is held without deadlocking.
type MemoryStore struct {
	root  string
	path  string
	mu    sync.Mutex
	govMu sync.Mutex
	gov   *Governance
}

// NewMemoryStore returns a typed memory store for the given project root.
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
	// Governance: when retention is enabled, every write enforces
	// expiry/archive/trim so stale memories are dropped as soon as the store
	// is next touched. Explicit sweeps (ApplyRetention) report the counts.
	if gov := s.govSnapshot(); gov != nil && gov.Retention.Enabled {
		ms, _ = gov.Retention.Apply(ms, time.Now().UTC())
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(ms, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.path, b, 0o600)
}

// Add stores a new typed memory entry. Returns the entry with ID and
// timestamps set.
//
// With governance attached, the writer is identified by m.Source ("human"
// when empty) and must hold write permission.
func (s *MemoryStore) Add(m domain.Memory) (domain.Memory, error) {
	if strings.TrimSpace(m.Content) == "" {
		return m, nil
	}
	agent := m.Source
	if agent == "" {
		agent = "human"
	}
	if err := s.authorize(agent, PermissionWrite); err != nil {
		return domain.Memory{}, err
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
	// A newly added memory is the authoritative, current entry ;
	// an explicit status is preserved for promoted entries.
	if m.Status == "" {
		m.Status = domain.MemoryCurrent
	}
	ms = append(ms, m)
	// FIFO eviction: keep only the newest maxTypedEntries, dropping the oldest.
	if len(ms) > maxTypedEntries {
		ms = ms[len(ms)-maxTypedEntries:]
	}
	if err := s.save(ms); err != nil {
		return domain.Memory{}, err
	}
	s.recordAudit(AuditEvent{
		AgentID:   agent,
		Operation: OpAdd,
		MemoryID:  m.ID,
		Type:      m.Type,
		Scope:     m.Scope,
		Allowed:   true,
	})
	return m, nil
}

// Supersede marks an older memory with the same type+scope as superseded and
// promotes a new one to current ( memory supersession). This makes
// the newest memory authoritative while retaining the older one for audit.
// The new memory may be an existing entry (promote) or a fresh one (replace).
//
// With governance attached, the writer is identified by newMemory.Source
// ("human" when empty) and must hold write permission.
func (s *MemoryStore) Supersede(newMemory domain.Memory) (domain.Memory, error) {
	if strings.TrimSpace(newMemory.Content) == "" {
		return domain.Memory{}, nil
	}
	agent := newMemory.Source
	if agent == "" {
		agent = "human"
	}
	if err := s.authorize(agent, PermissionWrite); err != nil {
		return domain.Memory{}, err
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
	s.recordAudit(AuditEvent{
		AgentID:   agent,
		Operation: OpSupersede,
		MemoryID:  newMemory.ID,
		Type:      newMemory.Type,
		Scope:     newMemory.Scope,
		Allowed:   true,
	})
	return newMemory, nil
}

// CurrentMemories returns only the memories that are current (not superseded
// and not historical) for the given type. An empty type returns
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

// MarkHistorical retires a memory to the historical state ,
// removing it from the authoritative set without deleting it.
// With governance attached, the actor is "human" and must hold write
// permission.
func (s *MemoryStore) MarkHistorical(id string) error {
	if err := s.authorize("human", PermissionWrite); err != nil {
		return err
	}
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
	if err := s.save(ms); err != nil {
		return err
	}
	s.recordAudit(AuditEvent{
		AgentID:   "human",
		Operation: OpUpdate,
		MemoryID:  id,
		Allowed:   true,
		Reason:    "mark-historical",
	})
	return nil
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

// Delete removes a memory by ID. With governance attached, the actor is
// "human" and must hold delete permission.
func (s *MemoryStore) Delete(id string) error {
	if err := s.authorize("human", PermissionDelete); err != nil {
		return err
	}
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
	if err := s.save(kept); err != nil {
		return err
	}
	s.recordAudit(AuditEvent{
		AgentID:   "human",
		Operation: OpDelete,
		MemoryID:  id,
		Allowed:   true,
	})
	return nil
}

// Update modifies an existing memory's content/tags. With governance
// attached, the actor is "human" and must hold write permission.
func (s *MemoryStore) Update(id string, content string, tags []string) (domain.Memory, error) {
	if err := s.authorize("human", PermissionWrite); err != nil {
		return domain.Memory{}, err
	}
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
		s.recordAudit(AuditEvent{
			AgentID:   "human",
			Operation: OpUpdate,
			MemoryID:  id,
			Type:      ms[i].Type,
			Scope:     ms[i].Scope,
			Allowed:   true,
		})
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
	return base + "-" + strconv.Itoa(n)
}

// AuthorizedRecall recalls memories matching the query, filtered by the
// caller's security clearance. Memories with a Classification higher than the
// caller's clearance are excluded. This closes spec §41 F-55 (per-agent
// authorization for shared memory).
// clearanceLevels maps classification strings to numeric levels:
// "" (unclassified) = 0, "public" = 0, "internal" = 1,
// "confidential" = 2, "restricted" = 3
// A caller with clearance N can read memories with classification <= N.
// With governance attached, agentID must hold read permission; the recall is
// recorded in the audit trail.
func (s *MemoryStore) AuthorizedRecall(q Query, agentID string, clearance int) ([]domain.Memory, error) {
	if err := s.authorize(agentID, PermissionRead); err != nil {
		return nil, err
	}
	mems, err := s.recall(q, agentID, true)
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
