// Package retrieval implements the Progressive Disclosure Protocol
// (L1/L2/L3): it retrieves a project's symbols through escalating levels of
// detail — index summary, neighborhood, then verbatim source — while keeping
// every result attached to a stable, content-hashable Handle.
package retrieval

import (
	"sort"
	"strconv"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/evidence"
)

// Type identifies what a Handle points at.
type Type string

const (
	TypeSymbol Type = "symbol"
	TypeFile   Type = "file"
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
