// Package retrieval implements the Progressive Disclosure Protocol
// (L1/L2/L3): it retrieves a project's symbols through escalating levels of
// detail — index summary, neighborhood, then verbatim source — while keeping
// every result attached to a stable, content-hashable Handle.
package retrieval

import (
	"encoding/json"
	"github.com/JayveerPrajapati/kern/internal/evidence"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Type identifies what a Handle points at.
type Type string

const (
	TypeSymbol   Type = "symbol"
	TypeFile     Type = "file"
	TypeEnvelope Type = "envelope" // a silent-pipeline context envelope render
)

// Handle is a stable identity for a retrievable unit (symbol or file) at a
// specific location, carrying the metadata needed to decide whether a cached
// render is still fresh.
type Handle struct {
	ID          string            `json:"id"`
	Type        Type              `json:"type"`
	Name        string            `json:"name"`   // symbol name or file path
	Source      string            `json:"source"` // file path
	Line        int               `json:"line"`
	TokenCost   int               `json:"token_cost"`
	Confidence  float64           `json:"confidence"`
	ContentHash string            `json:"content_hash"` // SHA-256 of current content (staleness check)
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// NewHandle builds a handle whose ID is a content-stable digest of the
// identity tuple (type, name, source, line): the same symbol/file at the
// same location always yields the same ID across runs.
func NewHandle(t Type, name, source string, line, tokenCost int, confidence float64, contentHash string) *Handle {
	return &Handle{
		ID:          evidence.Digest(string(t) + "\x00" + name + "\x00" + source + "\x00" + strconv.Itoa(line)),
		Type:        t,
		Name:        name,
		Source:      source,
		Line:        line,
		TokenCost:   tokenCost,
		Confidence:  confidence,
		ContentHash: contentHash,
	}
}

// Registry is a thread-safe store of handles keyed by stable ID.
type Registry struct {
	mu      sync.RWMutex
	handles map[string]*Handle
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{handles: map[string]*Handle{}}
}

// handleTTL bounds how long persisted handles survive before expiring. The
// ID is content-stable across runs, so a stale handle would still resolve —
// but the source it points at may have moved; TTL keeps the store honest.
const handleTTL = 7 * 24 * time.Hour

// HandleStorePath returns the per-project persistent handle store path.
func HandleStorePath(root string) string {
	return filepath.Join(root, ".kern", "handles.json")
}

// Save persists every registered handle to the store at path (atomic write
// via rename). It is the persistence half of the handle lifecycle: the CLI
// runs retrieve and resolve in separate processes, so a handle created by
// `kern retrieve` must survive long enough for `kern resolve` to find it.
func (r *Registry) Save(path string) error {
	r.mu.RLock()
	now := time.Now()
	type entry struct {
		Handle       *Handle   `json:"handle"`
		RegisteredAt time.Time `json:"registered_at"`
	}
	entries := make([]entry, 0, len(r.handles))
	for _, h := range r.handles {
		entries = append(entries, entry{Handle: h, RegisteredAt: now})
	}
	r.mu.RUnlock()
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load merges persisted handles from the store at path into the registry,
// dropping entries older than handleTTL. Best-effort: a missing or corrupt
// store leaves the registry unchanged (resolve then fails with the usual
// "unknown handle" message).
func (r *Registry) Load(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	type entry struct {
		Handle       *Handle   `json:"handle"`
		RegisteredAt time.Time `json:"registered_at"`
	}
	var entries []entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	cutoff := time.Now().Add(-handleTTL)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range entries {
		if e.Handle == nil || e.Handle.ID == "" {
			continue
		}
		if !e.RegisteredAt.IsZero() && e.RegisteredAt.Before(cutoff) {
			continue
		}
		r.handles[e.Handle.ID] = e.Handle
	}
}

// Register inserts h, overwriting any existing handle with the same ID.
func (r *Registry) Register(h *Handle) {
	if h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handles[h.ID] = h
}

// Resolve returns the handle registered under id, if present.
func (r *Registry) Resolve(id string) (*Handle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handles[id]
	return h, ok
}

// Invalidate removes the handle registered under id, if present.
func (r *Registry) Invalidate(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.handles, id)
}

// Len returns the number of registered handles.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.handles)
}

// List returns all registered handles sorted by ID.
func (r *Registry) List() []*Handle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Handle, 0, len(r.handles))
	for _, h := range r.handles {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
