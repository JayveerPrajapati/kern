package govaudit

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// TestRotationResetsLongLivedChainHead pins the audit-rotate hazard found live
// on 2026-10-27: a long-lived writer (the MCP server holds one AuditLog for its
// whole lifetime) kept its in-memory chain head after the store was rotated,
// so its next entry chained to the ARCHIVED tail and the fresh chain failed
// verification at entry 1. refreshTailLocked must reset the head on an empty
// store.
func TestRotationResetsLongLivedChainHead(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".kern", "audit")
	newLog := func() *AuditLog {
		return NewAuditLog().WithStore(storage.NewLog(dir)).WithLockPath(filepath.Join(dir, ".lock"))
	}
	live := newLog()
	live.Record(AuditEntry{Action: "before-rotate", Resource: "test", Result: "ok"})

	if _, err := ArchiveAuditStore(root, 0); err != nil {
		t.Fatalf("ArchiveAuditStore: %v", err)
	}
	live.Record(AuditEntry{Action: "after-rotate", Resource: "test", Result: "ok"})

	fresh := newLog()
	if brk, verified := fresh.VerifyChainReport(); brk >= 0 {
		t.Fatalf("post-rotation chain broken at entry %d (verified %d) — the long-lived writer chained to the archived tail", brk+1, verified)
	}
}

// TestRepairChainStableWithCustomIDs pins the second live failure of
// 2026-10-27: the store's List() order is key-sorted, not append order, so an
// entry with a custom ID ("audit-rotate-check" sorts after "audit-audit-15")
// landed at a different position than its hash assumed. Repair must re-chain
// in READ order and place its marker by the same sort, otherwise the repaired
// chain fails verification immediately.
func TestRepairChainStableWithCustomIDs(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLog(dir)
	lock := filepath.Join(dir, ".lock")
	l := NewAuditLog().WithStore(store).WithLockPath(lock)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	if err := l.AppendExternal(entry("custom-check", "c")); err != nil {
		t.Fatalf("AppendExternal: %v", err)
	}

	first := l.All()[0]
	first.Hash = strings.Repeat("0", 64)
	data, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "audit-"+first.ID, data); err != nil {
		t.Fatal(err)
	}

	fresh := NewAuditLog().WithStore(store).WithLockPath(lock)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if _, err := fresh.RepairChain(); err != nil {
		t.Fatalf("RepairChain: %v", err)
	}

	check := NewAuditLog().WithStore(storage.NewLog(dir)).WithLockPath(lock)
	if _, err := check.Replay(); err != nil {
		t.Fatalf("Replay after repair: %v", err)
	}
	if brk, verified := check.VerifyChainReport(); brk >= 0 {
		t.Fatalf("custom-ID chain broken at entry %d (verified %d) after repair", brk+1, verified)
	}
}
