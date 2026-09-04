package duplication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
)

// fingerprintCacheVersion is the on-disk format version. Bump it to invalidate
// every existing cache when the schema changes.
const fingerprintCacheVersion = 1

// fingerprintCacheFile is the cache filename under .blueprint/fingerprint-cache/.
const fingerprintCacheFile = "fingerprints.json"

// fingerprintCache is the on-disk shape of the whole-root fingerprint cache.
// It maps repo-relative file paths (forward slashes) to the fingerprints kern
// emitted for that file, keyed by the SHA-256 content hash of the file bytes.
type fingerprintCache struct {
	Version int                              `json:"version"`
	Files   map[string]fingerprintCacheEntry `json:"files"`
}

// fingerprintCacheEntry holds the cached fingerprints for one file. The key
// composition is (file path, content hash): fingerprints are content-derived,
// so an identical content hash guarantees an identical fingerprint.
type fingerprintCacheEntry struct {
	ContentHash  string                   `json:"content_hash"`
	Fingerprints []kern.FingerprintRecord `json:"fingerprints"`
}

// fingerprintCacheDir returns the cache directory under the repo root.
func fingerprintCacheDir(root string) string {
	return filepath.Join(root, ".blueprint", "fingerprint-cache")
}

// fingerprintCachePath returns the JSON cache file under the repo root.
func fingerprintCachePath(root string) string {
	return filepath.Join(fingerprintCacheDir(root), fingerprintCacheFile)
}

// loadFingerprintCache reads the cache file. A missing or unreadable file
// yields an empty cache; a corrupt payload or a version mismatch yields an
// empty cache too (it is rebuilt on the next save).
func loadFingerprintCache(path string) fingerprintCache {
	data, err := os.ReadFile(path)
	if err != nil {
		return fingerprintCache{Version: fingerprintCacheVersion, Files: map[string]fingerprintCacheEntry{}}
	}
	var c fingerprintCache
	if err := json.Unmarshal(data, &c); err != nil || c.Version != fingerprintCacheVersion || c.Files == nil {
		return fingerprintCache{Version: fingerprintCacheVersion, Files: map[string]fingerprintCacheEntry{}}
	}
	return c
}

// saveFingerprintCache atomically writes the cache file (temp file + rename),
// creating the cache directory if needed. Any error is returned; callers treat
// it as best-effort and never fail the check on it.
func saveFingerprintCache(path string, c fingerprintCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".fingerprints-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// contentHash returns the lowercase hex SHA-256 of a file's bytes.
func contentHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// existingGoFiles returns the sorted repo-relative (forward-slash) paths of
// every .go file under root, excluding VCS/runtime directories (.git, .kern,
// .blueprint) and any path in exclude. Enumeration is best-effort: unreadable
// entries are skipped rather than aborting the walk.
func existingGoFiles(root string, exclude map[string]bool) ([]string, error) {
	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries (permission errors etc.)
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", ".kern", ".blueprint":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		if exclude != nil && (exclude[key] || exclude[rel]) {
			return nil
		}
		files = append(files, key)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// fingerprintExisting returns fingerprints for the existing (unchanged) files
// in repoRoot, excluding the changed files in changedSet.
//
// Caching: when .blueprint/fingerprint-cache/fingerprints.json exists, existing
// files are served from the cache (keyed by repo-relative path + SHA-256
// content hash) and only files whose content hash changed are re-fingerprinted,
// batched into a single scoped scan. When the cache file does not exist yet —
// or the cache directory is unavailable — it falls back to the uncached
// whole-root scan and best-effort-populates the cache for the next run.
//
// The cache never affects the result: a cache miss produces identical records
// to the uncached path, and any cache I/O failure silently degrades to the
// uncached behavior. Only kern scan errors fail the check, exactly as before.
func (c Check) fingerprintExisting(ctx context.Context, repoRoot string, changedSet map[string]bool) ([]funcWithLocation, error) {
	cachePath := fingerprintCachePath(repoRoot)
	cache := loadFingerprintCache(cachePath)

	// No cache file yet (first run, or the cache dir is unavailable): run the
	// uncached whole-root scan and best-effort-populate the cache so the next
	// run is cache-served.
	if _, err := os.Stat(cachePath); err != nil {
		existingFuncs, recs, err := c.fingerprintExistingUncached(ctx, repoRoot, changedSet)
		if err != nil {
			return nil, err
		}
		populateFingerprintCache(cachePath, repoRoot, changedSet, recs)
		return existingFuncs, nil
	}

	// Cached path: hash every existing file; serve cache hits and collect the
	// misses (changed files) into one batched scoped scan.
	files, err := existingGoFiles(repoRoot, changedSet)
	if err != nil {
		// Cannot enumerate existing files: fall back to the uncached scan
		// rather than failing the check on a cache-side problem.
		existingFuncs, _, err := c.fingerprintExistingUncached(ctx, repoRoot, changedSet)
		if err != nil {
			return nil, err
		}
		return existingFuncs, nil
	}

	existingFuncs := make([]funcWithLocation, 0, len(files))
	var missFiles []string
	missHashes := make(map[string]string, len(files))
	for _, file := range files {
		hash, err := contentHash(filepath.Join(repoRoot, filepath.FromSlash(file)))
		if err != nil {
			continue // unreadable file: kern would skip it too
		}
		if entry, ok := cache.Files[file]; ok && entry.ContentHash == hash {
			for _, rec := range entry.Fingerprints {
				existingFuncs = append(existingFuncs, funcWithLocation{
					Fingerprint: fingerprintFromRecord(rec),
					File:        rec.File,
					Line:        rec.Line,
				})
			}
			continue
		}
		missFiles = append(missFiles, file)
		missHashes[file] = hash
	}

	if len(missFiles) > 0 {
		// Batch the cache-miss files into a single scoped scan; the KernClient
		// chunks the set internally when it exceeds its batch size. Scan
		// failures fail the check exactly like the uncached path — the cache
		// never hides a kern error.
		recs, err := c.client.Fingerprints(ctx, repoRoot, missFiles)
		if err != nil {
			return nil, err
		}
		byFile := make(map[string][]kern.FingerprintRecord, len(missFiles))
		for _, rec := range recs {
			byFile[rec.File] = append(byFile[rec.File], rec)
			existingFuncs = append(existingFuncs, funcWithLocation{
				Fingerprint: fingerprintFromRecord(rec),
				File:        rec.File,
				Line:        rec.Line,
			})
		}
		for _, file := range missFiles {
			cache.Files[file] = fingerprintCacheEntry{
				ContentHash:  missHashes[file],
				Fingerprints: byFile[file],
			}
		}
	}

	// Drop cache entries for files that no longer exist on disk.
	live := make(map[string]bool, len(files))
	for _, f := range files {
		live[f] = true
	}
	for key := range cache.Files {
		if !live[key] {
			delete(cache.Files, key)
		}
	}

	_ = saveFingerprintCache(cachePath, cache)
	return existingFuncs, nil
}

// fingerprintExistingUncached reproduces the uncached behavior exactly: a
// whole-root fingerprint scan of the repo root, filtered to exclude the
// changed files. It returns both the filtered funcs and the raw scan records
// (the raw records let callers populate the cache from the scan).
func (c Check) fingerprintExistingUncached(ctx context.Context, repoRoot string, changedSet map[string]bool) ([]funcWithLocation, []kern.FingerprintRecord, error) {
	recs, err := c.client.Fingerprints(ctx, repoRoot, nil)
	if err != nil {
		return nil, nil, err
	}
	existingFuncs := make([]funcWithLocation, 0, len(recs))
	for _, rec := range recs {
		if changedSet[rec.File] {
			continue
		}
		existingFuncs = append(existingFuncs, funcWithLocation{
			Fingerprint: fingerprintFromRecord(rec),
			File:        rec.File,
			Line:        rec.Line,
		})
	}
	return existingFuncs, recs, nil
}

// populateFingerprintCache seeds the cache with per-file entries derived from a
// whole-root scan so the next run is cache-served. Best-effort: any failure to
// hash, walk, or persist is ignored (the cache must never fail the check).
func populateFingerprintCache(cachePath, repoRoot string, changedSet map[string]bool, recs []kern.FingerprintRecord) {
	cache := fingerprintCache{Version: fingerprintCacheVersion, Files: map[string]fingerprintCacheEntry{}}
	files, err := existingGoFiles(repoRoot, changedSet)
	if err != nil {
		return
	}
	byFile := make(map[string][]kern.FingerprintRecord, len(recs))
	for _, rec := range recs {
		byFile[rec.File] = append(byFile[rec.File], rec)
	}
	for _, file := range files {
		hash, err := contentHash(filepath.Join(repoRoot, filepath.FromSlash(file)))
		if err != nil {
			continue
		}
		cache.Files[file] = fingerprintCacheEntry{ContentHash: hash, Fingerprints: byFile[file]}
	}
	_ = saveFingerprintCache(cachePath, cache)
}
