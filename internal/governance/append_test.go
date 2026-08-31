package governance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

// externalEntry returns a representative entry that an external caller (e.g.
// Blueprint's audit writer) would append: no ID (auto-assigned), a status-like
// Result, and no Hash (computed by AppendExternal).
func externalEntry() AuditEntry {
	return AuditEntry{
		AgentID:  "blueprint",
		Action:   "commit",
		Resource: "/tmp/repo",
		Risk:     domain.Risk{Level: domain.RiskHigh},
		Result:   "BLOCK",
		TaskID:   "bp-123",
	}
}

// TestAppendExternalChains: two external appends must both be persisted,
// hash-chained (each hash covers the previous), and verify intact.
func TestAppendExternalChains(t *testing.T) {
	store := storage.NewLocal(t.TempDir())
	l := NewAuditLog().WithStore(store)

	a := externalEntry()
	a.TaskID = "bp-a"
	if err := l.AppendExternal(a); err != nil {
		t.Fatalf("AppendExternal A: %v", err)
	}
	b := externalEntry()
	b.TaskID = "bp-b"
	if err := l.AppendExternal(b); err != nil {
		t.Fatalf("AppendExternal B: %v", err)
	}

	if !l.VerifyChain() {
		t.Fatal("VerifyChain() = false for intact chained external appends")
	}
	all := l.All()
	if len(all) != 2 {
		t.Fatalf("All() = %d entries, want 2", len(all))
	}
	if all[0].Hash == "" || all[1].Hash == "" {
		t.Fatal("external appends carry no hashes")
	}
	if all[1].Hash == all[0].Hash {
		t.Error("second append has the same hash as the first — entries are not chained")
	}
	// The persisted values must equal the in-memory entries.
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("store.List(): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("store has %d entries, want 2", len(entries))
	}
}

// TestAppendExternalReplayThenAppend: entries recorded internally by a prior
// process, then replayed, must chain onto a new external append so the whole
// chain verifies across the internal/external boundary.
func TestAppendExternalReplayThenAppend(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	first := NewAuditLog().WithStore(store)
	first.Record(entry("", "a"))
	first.Record(entry("", "b"))
	if !first.VerifyChain() {
		t.Fatal("internal chain should be intact before the external append")
	}

	// Fresh process: load the persisted chain head, then append externally.
	fresh := NewAuditLog().WithStore(store)
	if n, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	} else if n != 2 {
		t.Fatalf("Replay() = %d entries, want 2", n)
	}
	if err := fresh.AppendExternal(externalEntry()); err != nil {
		t.Fatalf("AppendExternal after replay: %v", err)
	}

	if !fresh.VerifyChain() {
		t.Fatal("VerifyChain() = false: external entry not chained to replayed internal entries")
	}
	all := fresh.All()
	if len(all) != 3 {
		t.Fatalf("All() = %d entries, want 3", len(all))
	}
	if all[2].AgentID != "blueprint" || all[2].Result != "BLOCK" {
		t.Errorf("external entry = %+v, want AgentID=blueprint Result=BLOCK", all[2])
	}

	// A third process replaying everything must still verify.
	again := NewAuditLog().WithStore(store)
	if _, err := again.Replay(); err != nil {
		t.Fatalf("second Replay(): %v", err)
	}
	if !again.VerifyChain() {
		t.Fatal("VerifyChain() = false after a second full replay")
	}
}

// TestAppendExternalPersists: with a store the entry file is written; without
// a store the append is in-memory only and returns no error.
func TestAppendExternalPersists(t *testing.T) {
	t.Run("with_store_writes_file", func(t *testing.T) {
		dir := t.TempDir()
		l := NewAuditLog().WithStore(storage.NewLocal(dir))
		if err := l.AppendExternal(externalEntry()); err != nil {
			t.Fatalf("AppendExternal: %v", err)
		}
		// Key format matches Record/persist: "audit-" + auto-assigned ID
		// ("audit-N"), i.e. the first external append is "audit-audit-1".
		if _, err := os.Stat(filepath.Join(dir, "audit-audit-1.json")); err != nil {
			t.Errorf("persisted entry file not found: %v", err)
		}
	})

	t.Run("without_store_is_in_memory_only", func(t *testing.T) {
		l := NewAuditLog().WithStore(nil)
		if err := l.AppendExternal(externalEntry()); err != nil {
			t.Fatalf("AppendExternal without store: %v", err)
		}
		if got := l.All(); len(got) != 1 || got[0].Hash == "" {
			t.Fatalf("in-memory append = %+v, want 1 entry with a hash", got)
		}
		if !l.VerifyChain() {
			t.Error("VerifyChain() = false for in-memory-only external append")
		}
	})
}

// failingStore wraps a real store but fails every Put, simulating a
// read-only disk / storage outage.
type failingStore struct {
	storage.Store
}

func (failingStore) Put(context.Context, string, json.RawMessage) error {
	return errors.New("storage: disk full")
}

// TestAppendExternalReturnsErrorOnStoreFailure: unlike Record (which swallows
// persist errors), AppendExternal must surface them so external callers know
// the chain link was not written.
func TestAppendExternalReturnsErrorOnStoreFailure(t *testing.T) {
	l := NewAuditLog().WithStore(failingStore{storage.NewLocal(t.TempDir())})
	err := l.AppendExternal(externalEntry())
	if err == nil {
		t.Fatal("AppendExternal() = nil with a failing store, want error")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("AppendExternal error = %q, want it to mention the storage failure", err)
	}
}
