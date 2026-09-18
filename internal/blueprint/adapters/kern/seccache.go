package kern

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/fsutil"
)

// contentHash returns a short sha256 fingerprint of file content, used to
// distinguish a real edit (hash changes → partial re-scan) from a touch that
// preserves content (hash matches → replay from cache).
func contentHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// secCache is the on-disk cache of per-file secret-scan findings. `kern sec`
// only scans whole directories, so repeat validations over unchanged staged
// files would re-scan the entire repo every time. The cache keys findings by
// file identity — stat (size + mtime) for the zero-read fast path, plus a
// content hash so a touch that preserves content (git checkout, editors
// rewriting identical bytes) still replays instead of forcing a whole-repo
// rescan. A file whose content genuinely changed is re-scanned alone from a
// temp dir (partial miss), never by re-scanning the whole repository.
type secCache struct {
	Version int                      `json:"version"`
	Files   map[string]secCacheEntry `json:"files"`
}

// secCacheEntry is the cached state for one file: the file's identity at scan
// time (stat + content hash) plus every finding kern reported for it (raw
// SecFinding, allowlist and changed-file filtering happen at replay time, not
// cache time).
type secCacheEntry struct {
	Size        int64        `json:"size"`
	MTimeNS     int64        `json:"mtime_ns"`
	ContentHash string       `json:"content_hash"`
	Findings    []SecFinding `json:"findings"`
}

// secCacheVersion guards against loading a cache written by a different
// Blueprint build with a different SecFinding shape or cache-entry shape.
const secCacheVersion = 2

// secCachePath returns the cache file location under the repo root.
func secCachePath(root string) string {
	return filepath.Join(root, ".blueprint", "sec-cache.json")
}

// loadSecCache reads the cache at path. A missing or corrupt cache returns an
// empty cache and never an error (best-effort, same spirit as metrics): a
// broken or partial cache must never fail a validation.
func loadSecCache(path string) secCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return secCache{Files: map[string]secCacheEntry{}}
	}
	var c secCache
	if err := json.Unmarshal(data, &c); err != nil || c.Version != secCacheVersion || c.Files == nil {
		return secCache{Files: map[string]secCacheEntry{}}
	}
	return c
}

// saveSecCache writes the cache atomically (temp file + rename, mirroring
// metrics.go's pattern): a concurrent reader or process never observes a
// half-written cache file. Callers treat save failures as best-effort.
func saveSecCache(path string, c secCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}
