package incident

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/domain"
)

// Store persists incidents per project so the Web Console and the
// closed-loop learning step can list and correlate historical
// incidents across sessions. It mirrors the memory store persistence pattern
// (JSON under the project cache, additive, atomic temp-file rename).
type Store struct {
	root string
	path string

	mu sync.Mutex // guards all read/modify/write paths to prevent lost updates
}

// NewStore returns an incident store for the given project root.
// Storage path: <cache_dir>/incidents/<project_hash>.json
func NewStore(root string) *Store {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	path := cache.Path("incidents", cache.Hash([]byte(abs))+".json")
	return &Store{root: root, path: path}
}

func (s *Store) load() []domain.Incident {
	var list []domain.Incident
	b, err := os.ReadFile(s.path)
	if err != nil {
		return []domain.Incident{}
	}
	if err := json.Unmarshal(b, &list); err != nil {
		// Surface the corruption instead of silently dropping the stored data.
		log.Printf("incident: store load %s: unmarshal: %v", s.path, err)
	}
	if list == nil {
		list = []domain.Incident{}
	}
	return list
}

func (s *Store) save(list []domain.Incident) error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	// Unique temp name so concurrent writers never clobber each other.
	tmp, err := os.CreateTemp(dir, "*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

// randomIncidentID returns a cryptographically random incident ID of the form
// "inc-<hex>", using 8 random bytes (16 hex chars). crypto/rand guarantees the
// ID is unpredictable, so concurrent requests can never collide on the same ID
// (unlike a timestamp-based ID).
func randomIncidentID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on supported platforms; fall back to
		// zero bytes (still a valid, unique-enough ID shape) rather than panic.
		return "inc-0000000000000000"
	}
	return "inc-" + hex.EncodeToString(b[:])
}

// Save persists an incident (insert or replace by ID), returning the stored
// record with UpdatedAt stamped. When the incident has no ID, one is generated
// here under the mutex so concurrent saves never produce colliding IDs.
func (s *Store) Save(inc *domain.Incident) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if inc.ID == "" {
		inc.ID = randomIncidentID()
	}
	inc.UpdatedAt = time.Now().UTC()
	list := s.load()
	kept := list[:0]
	for _, it := range list {
		if it.ID != inc.ID {
			kept = append(kept, it)
		}
	}
	kept = append(kept, *inc)
	if err := s.save(kept); err != nil {
		return domain.Incident{}, err
	}
	return *inc, nil
}

// List returns all persisted incidents, newest first.
func (s *Store) List() ([]domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := s.load()
	// Most recent first.
	for i := 0; i < len(list)/2; i++ {
		j := len(list) - 1 - i
		list[i], list[j] = list[j], list[i]
	}
	return list, nil
}

// Get returns an incident by ID, or os.ErrNotExist.
func (s *Store) Get(id string) (domain.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, it := range s.load() {
		if it.ID == id {
			return it, nil
		}
	}
	return domain.Incident{}, os.ErrNotExist
}
