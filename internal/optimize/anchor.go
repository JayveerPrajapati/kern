// Package optimize anchor store holds raw context segments truncated by kern
// so AI agents can hydrate them on demand using an anchor identifier.
package optimize

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

var (
	anchorMu    sync.RWMutex
	anchorStore = make(map[string]string)
)

// StoreAnchor saves an omitted text block and returns its deterministic anchor ID.
func StoreAnchor(text string) string {
	if text == "" {
		return ""
	}
	h := sha256.Sum256([]byte(text))
	id := "anchor-" + hex.EncodeToString(h[:])[:12]
	anchorMu.Lock()
	anchorStore[id] = text
	anchorMu.Unlock()
	return id
}

// FetchAnchor retrieves a stored uncompressed text block by anchor ID.
func FetchAnchor(id string) (string, error) {
	anchorMu.RLock()
	defer anchorMu.RUnlock()
	val, ok := anchorStore[id]
	if !ok {
		return "", fmt.Errorf("anchor %q not found or expired", id)
	}
	return val, nil
}

// ClearAnchors resets the in-memory anchor store.
func ClearAnchors() {
	anchorMu.Lock()
	anchorStore = make(map[string]string)
	anchorMu.Unlock()
}
