package govaudit

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/storage"
)

// A persisted zero-valued record at the head of the chain (key "audit-",
// empty ID/Hash/Timestamp) must not be replayed as an audit entry: its empty
// hash can never verify, which pinned the live chain at "broken at entry 1"
// forever (2026-11-10 campaign). A metadata envelope ({k,v} with no identity)
// is likewise not an entry.
func TestReplaySkipsZeroValuedAndMetadataRecords(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".kern", "audit")
	store := storage.NewLog(dir)
	ctx := context.Background()

	if err := store.Put(ctx, "audit-", []byte(`{"ID":"","Timestamp":"0001-01-01T00:00:00Z"}`)); err != nil {
		t.Fatalf("put ghost: %v", err)
	}
	if err := store.Put(ctx, "rotation", []byte(`{"k":"audit-","v":{"rotated":true}}`)); err != nil {
		t.Fatalf("put metadata: %v", err)
	}

	log := NewAuditLog().WithStore(store).WithLockPath(filepath.Join(dir, ".lock"))
	n, err := log.Replay()
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if n != 0 {
		t.Fatalf("replay loaded %d entries, want 0 (ghost + metadata skipped)", n)
	}
	if all := log.All(); len(all) != 0 {
		t.Fatalf("All() = %d entries, want 0", len(all))
	}
	if brk, _ := log.VerifyChainReport(); brk >= 0 {
		t.Fatalf("ghost-only chain reports broken at %d, want intact (-1)", brk)
	}
}
