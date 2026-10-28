package govaudit

import (
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestAppendExternalCustomIDThenFreshReplayVerifies pins the live P0-2 break
// of 2026-10-27 (audit entry 24, audit-rotate-check). An external append with
// a caller-supplied custom ID is written at the TRUE TAIL of the append log
// and hashed against that tail; later numeric entries chain from it. Replay
// must therefore preserve the chain file's physical append order instead of
// re-sorting numeric IDs first and custom IDs last — otherwise every entry
// written after the custom ID fails verification until a human repairs it.
//
// This is the append → fresh replay → verify path with NO repair step.
func TestAppendExternalCustomIDThenFreshReplayVerifies(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLog(dir)
	lock := filepath.Join(dir, ".lock")

	l := NewAuditLog().WithStore(store).WithLockPath(lock)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	if err := l.AppendExternal(entry("custom-check", "c")); err != nil {
		t.Fatalf("AppendExternal: %v", err)
	}
	// A later auto-ID entry chains from the custom-ID tail. This is the entry
	// that broke on replay before the fix.
	l.Record(entry("", "d"))

	fresh := NewAuditLog().WithStore(store).WithLockPath(lock)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if brk, verified := fresh.VerifyChainReport(); brk >= 0 {
		t.Fatalf("custom-ID append broke the chain at entry %d (verified %d of %d) on fresh replay — write order was not preserved",
			brk+1, verified, len(fresh.All()))
	}
}
