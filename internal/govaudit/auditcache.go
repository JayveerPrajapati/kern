package govaudit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/auditdiag"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// CachedChainVerdict verifies the project's on-disk audit chain at most once
// per chain state. Before this cache existed, every command that built the
// platform walked (and HMAC-checked) the whole chain THREE times — seconds of
// startup latency for read-only work, and the reason `kern plan`/`kern meta`
// felt slow on a mature repo (2026-11-10 campaign).
//
// The cache is keyed by the chain file's size+mtime, so an append, rotation,
// repair, or any edit invalidates it and forces a fresh walk: a stale cache
// can never hide a change to the chain.
func CachedChainVerdict(dir string) auditdiag.VerifyCache {
	stamp := chainStamp(dir)
	if v, ok := auditdiag.ReadVerifyCacheDir(dir); ok && v.Stamp == stamp {
		return v
	}
	l := NewAuditLog().
		WithStore(storage.NewLog(dir)).
		WithLockPath(filepath.Join(dir, ".lock"))
	// verifyChainLocked walks the IN-MEMORY entries, so the persisted chain
	// must be replayed first (this is the cheap JSON read; the expensive part
	// is the per-entry HMAC walk the cache exists to skip).
	if _, err := l.Replay(); err != nil {
		return auditdiag.VerifyCache{Stamp: stamp, FirstBroken: -1}
	}
	l.mu.Lock()
	firstBroken, verified, tiers := l.verifyChainLocked()
	l.mu.Unlock()
	v := auditdiag.VerifyCache{
		Stamp:            stamp,
		FirstBroken:      firstBroken,
		BrokenCount:      tiers.Broken,
		Verified:         verified,
		Total:            tiers.Total,
		Keyed:            tiers.Keyed,
		Plain:            tiers.Plain,
		Legacy:           tiers.Legacy,
		Downgraded:       tiers.Downgraded,
		TailUnkeyed:      tiers.TailDowngraded,
		FirstTailUnkeyed: tiers.FirstTailDowngraded,
		CheckedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	writeVerifyCache(dir, v)
	return v
}

// chainStamp identifies the chain state cheaply: entry count changes the
// size, edits change the mtime. An absent chain gets a stable "missing".
func chainStamp(dir string) string {
	fi, err := os.Stat(filepath.Join(dir, "chain.jsonl"))
	if err != nil {
		return "missing"
	}
	return fmt.Sprintf("%d-%d", fi.Size(), fi.ModTime().UnixNano())
}

// writeVerifyCache persists the verdict atomically; best-effort (a read-only
// store must not fail a command that only wanted to verify).
func writeVerifyCache(dir string, v auditdiag.VerifyCache) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := auditdiag.VerifyCacheDir(dir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
