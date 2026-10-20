// Package optimize anchor store holds raw context segments truncated by kern
// so AI agents can hydrate them on demand using an anchor identifier. The
// store is bounded (maxAnchors entries, LRU eviction) so a long-lived
// process cannot grow it without bound.
package optimize

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
)

// maxAnchors bounds the in-memory anchor store. Once the store exceeds it,
// the least recently used entries are evicted (see StoreAnchor).
const maxAnchors = 1024

var (
	anchorMu    sync.Mutex
	anchorStore = make(map[string]string)
	anchorOrder = list.New() // LRU order: front = most recent, back = eviction candidate
)

// StoreAnchor saves an omitted text block and returns its deterministic anchor ID.
// The store is capped at maxAnchors entries: new ids are inserted at the front
// of the LRU order and the oldest (back) entries are evicted beyond the cap.
func StoreAnchor(text string) string {
	if text == "" {
		return ""
	}
	h := sha256.Sum256([]byte(text))
	id := "anchor-" + hex.EncodeToString(h[:])[:12]
	anchorMu.Lock()
	if _, exists := anchorStore[id]; !exists {
		anchorOrder.PushFront(id)
	} else {
		// Deterministic id already resident: refresh its LRU position so
		// repeated stores keep the anchor hot.
		for e := anchorOrder.Front(); e != nil; e = e.Next() {
			if e.Value.(string) == id {
				anchorOrder.MoveToFront(e)
				break
			}
		}
	}
	anchorStore[id] = text
	for anchorOrder.Len() > maxAnchors {
		back := anchorOrder.Back()
		delete(anchorStore, back.Value.(string))
		anchorOrder.Remove(back)
	}
	anchorMu.Unlock()
	return id
}

// FetchAnchor retrieves a stored uncompressed text block by anchor ID. A hit
// refreshes the anchor's LRU position so frequently used anchors stay
// resident.
func FetchAnchor(id string) (string, error) {
	anchorMu.Lock()
	defer anchorMu.Unlock()
	val, ok := anchorStore[id]
	if !ok {
		return "", fmt.Errorf("anchor %q not found or expired", id)
	}
	for e := anchorOrder.Front(); e != nil; e = e.Next() {
		if e.Value.(string) == id {
			anchorOrder.MoveToFront(e)
			break
		}
	}
	return val, nil
}

// ClearAnchors resets the in-memory anchor store (map and LRU order).
func ClearAnchors() {
	anchorMu.Lock()
	anchorStore = make(map[string]string)
	anchorOrder.Init()
	anchorMu.Unlock()
}
