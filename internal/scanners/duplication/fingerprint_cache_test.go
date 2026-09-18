package duplication

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/adapters/kern"
	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
)

// fpRecord builds a minimal fingerprint record for cache tests. The records
// only need a stable path/name — the fake runner serves them regardless of the
// file bytes, so cache mechanics (not similarity) are what these tests assert.
func fpRecord(file, name string) kern.FingerprintRecord {
	return kern.FingerprintRecord{
		File:           file,
		Name:           name,
		SignatureShape: "func()",
		Lang:           "go",
		Line:           1,
	}
}

// newCacheTestRepo materializes a fixture repo with two existing .go files and
// a third file that acts as the staged change, and returns the repo root, the
// counting fake client, and the change request. All three files exist on disk
// so the whole-root scan on the first run finds them all.
func newCacheTestRepo(t *testing.T) (string, *fingerprintFake, domain.ChangeRequest) {
	t.Helper()
	dir := t.TempDir()
	writeGoMod(t, dir)
	writeDupFile(t, dir, "shared/retry.go", sharedRetrySource(t))
	writeDupFile(t, dir, "shared/process.go", `package shared

// Process is a standalone function used to exercise the fingerprint cache.
func Process(in []byte) error { return nil }
`)
	writeDupFile(t, dir, "payments/retry.go", paymentsRetrySource(t))

	records := fixtureFingerprintRecords("exact-duplicate")
	records["shared/process.go"] = []kern.FingerprintRecord{
		fpRecord("shared/process.go", "Process"),
	}
	fake := &fingerprintFake{records: records}

	req := domain.ChangeRequest{
		RepositoryRoot: dir,
		Source:         domain.SourceCI,
		Operation:      domain.OpCommit,
		Files:          []domain.FileChange{{Path: "payments/retry.go", Op: domain.OpWrite}},
	}
	return dir, fake, req
}

// runDupCheck runs the duplication check once and fails the test on any error
// (returning the result so callers can compare runs).
func runDupCheck(t *testing.T, fake *fingerprintFake, req domain.ChangeRequest) domain.CheckResult {
	t.Helper()
	check := newFakeCheck(fake)
	res, err := check.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status == domain.StatusError {
		t.Fatalf("check errored: %s", res.Error)
	}
	return res
}

// TestFingerprintCacheHit verifies that a second run over unchanged files makes
// zero kern fingerprint calls for existing files: every existing file is served
// from the cache, and only the changed (staged) file is fingerprinted.
func TestFingerprintCacheHit(t *testing.T) {
	dir, fake, req := newCacheTestRepo(t)

	first := runDupCheck(t, fake, req) // run 1: populates the cache
	firstRunCalls := len(fake.calls)

	second := runDupCheck(t, fake, req) // run 2: unchanged files -> cache-served
	secondRunCalls := fake.calls[firstRunCalls:]

	if len(secondRunCalls) != 1 {
		t.Fatalf("second run made %d kern fingerprint calls, want 1 (only the changed file): %+v", len(secondRunCalls), secondRunCalls)
	}
	for _, call := range secondRunCalls {
		if call.files == nil {
			t.Errorf("second run did a whole-root fingerprint scan; the cache should have served existing files")
			continue
		}
		for _, f := range call.files {
			if f != "payments/retry.go" {
				t.Errorf("second run fingerprinted existing file %q; want only the changed file", f)
			}
		}
	}

	// The cache file exists with one entry per existing file, keyed by
	// (path, SHA-256 content hash).
	cache := loadFingerprintCache(fingerprintCachePath(dir))
	if len(cache.Files) != 2 {
		t.Fatalf("cache has %d entries, want 2: %+v", len(cache.Files), cache.Files)
	}
	for _, f := range []string{"shared/retry.go", "shared/process.go"} {
		entry, ok := cache.Files[f]
		if !ok {
			t.Errorf("cache missing entry for %s", f)
			continue
		}
		hash, err := contentHash(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatalf("contentHash(%s): %v", f, err)
		}
		if entry.ContentHash != hash {
			t.Errorf("cache content hash for %s = %q, want %q (path + SHA-256 of bytes)", f, entry.ContentHash, hash)
		}
	}

	// A cache-served run must produce identical results to the uncached run.
	if second.Status != first.Status {
		t.Errorf("status changed between runs: first=%s second=%s", first.Status, second.Status)
	}
	if len(second.Findings) != len(first.Findings) {
		t.Errorf("findings changed between runs: first=%d second=%d", len(first.Findings), len(second.Findings))
	}
}

// TestFingerprintCacheMissOnChange verifies that after an existing file's
// content changes, only that file is re-fingerprinted; the rest are cache hits.
func TestFingerprintCacheMissOnChange(t *testing.T) {
	dir, fake, req := newCacheTestRepo(t)

	runDupCheck(t, fake, req) // run 1: populates the cache
	firstRunCalls := len(fake.calls)

	// Modify an existing file: its content hash changes -> cache miss.
	writeDupFile(t, dir, "shared/retry.go", `package shared

// RetryRequest changed: different bytes, so a different content hash.
func RetryRequest() error { return nil }
`)

	runDupCheck(t, fake, req) // run 2
	secondRunCalls := fake.calls[firstRunCalls:]

	var reFingerprinted []string
	for _, call := range secondRunCalls {
		if call.files == nil {
			t.Errorf("second run did a whole-root fingerprint scan; the cache should have served the unchanged files")
			continue
		}
		reFingerprinted = append(reFingerprinted, call.files...)
	}

	// Exactly two scoped scans: the changed file + the modified existing file.
	if len(reFingerprinted) != 2 {
		t.Fatalf("second run fingerprinted %v, want exactly the modified existing file and the changed file", reFingerprinted)
	}
	foundModified := false
	for _, f := range reFingerprinted {
		switch f {
		case "shared/retry.go":
			foundModified = true
		case "shared/process.go":
			t.Errorf("unchanged existing file shared/process.go was re-fingerprinted")
		}
	}
	if !foundModified {
		t.Errorf("modified existing file shared/retry.go was not re-fingerprinted; calls: %+v", secondRunCalls)
	}
}

// TestFingerprintCacheDeletedFile verifies that a file deleted from disk has
// its cache entry removed on the next run, and that the run does not error.
func TestFingerprintCacheDeletedFile(t *testing.T) {
	dir, fake, req := newCacheTestRepo(t)

	runDupCheck(t, fake, req) // run 1: populates the cache

	cachePath := fingerprintCachePath(dir)
	cache := loadFingerprintCache(cachePath)
	if _, ok := cache.Files["shared/process.go"]; !ok {
		t.Fatalf("setup: cache missing entry for shared/process.go: %+v", cache.Files)
	}

	if err := os.Remove(filepath.Join(dir, "shared", "process.go")); err != nil {
		t.Fatalf("remove shared/process.go: %v", err)
	}

	runDupCheck(t, fake, req) // run 2: must not error

	cache = loadFingerprintCache(cachePath)
	if _, ok := cache.Files["shared/process.go"]; ok {
		t.Errorf("deleted file shared/process.go still has a cache entry")
	}
	if _, ok := cache.Files["shared/retry.go"]; !ok {
		t.Errorf("surviving file shared/retry.go lost its cache entry")
	}
}
