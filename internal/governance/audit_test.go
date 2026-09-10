package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

func entry(id, agentID string) AuditEntry {
	return AuditEntry{ID: id, AgentID: agentID, Action: "write", Resource: "source",
		Risk: domain.Risk{Level: domain.RiskMedium}, Result: "allowed"}
}

func TestNewAuditLogEmpty(t *testing.T) {
	l := NewAuditLog()
	if got := l.All(); len(got) != 0 {
		t.Errorf("All() = %d entries, want 0", len(got))
	}
	if got := l.Filter("agent-1"); len(got) != 0 {
		t.Errorf("Filter() = %d entries, want 0", len(got))
	}
	if !l.VerifyChain() {
		t.Error("VerifyChain() = false for empty in-memory log, want true")
	}
}

func TestRecordAssignsIDAndTimestamp(t *testing.T) {
	l := NewAuditLog()
	l.Record(AuditEntry{AgentID: "a1", Action: "write", Resource: "source",
		Risk: domain.Risk{Level: domain.RiskMedium}, Result: "allowed"})
	all := l.All()
	if len(all) != 1 {
		t.Fatalf("All() = %d, want 1", len(all))
	}
	if all[0].ID == "" {
		t.Error("Record should assign an ID when absent")
	}
	if !strings.HasPrefix(all[0].ID, "audit-") {
		t.Errorf("assigned ID = %q, want audit- prefix", all[0].ID)
	}
	if all[0].Timestamp.IsZero() {
		t.Error("Record should assign a timestamp when absent")
	}
	if all[0].AgentID != "a1" {
		t.Errorf("AgentID = %q, want a1", all[0].AgentID)
	}
}

func TestRecordSequentialIDs(t *testing.T) {
	l := NewAuditLog()
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	all := l.All()
	if all[0].ID == all[1].ID {
		t.Error("sequential auto-assigned IDs should be distinct")
	}
}

func TestRecordPreservesProvidedFields(t *testing.T) {
	l := NewAuditLog()
	l.Record(AuditEntry{ID: "custom-id", AgentID: "a", Action: "drop", Resource: "database",
		Risk: domain.Risk{Level: domain.RiskCritical}, Approved: true, Result: "blocked"})
	got := l.All()[0]
	if got.ID != "custom-id" {
		t.Errorf("ID = %q, want custom-id", got.ID)
	}
	if !got.Approved || got.Result != "blocked" {
		t.Errorf("Approved/Result = %v/%q", got.Approved, got.Result)
	}
}

func TestAllPreservesOrder(t *testing.T) {
	l := NewAuditLog()
	l.Record(entry("1", "a"))
	l.Record(entry("2", "b"))
	l.Record(entry("3", "a"))
	all := l.All()
	if len(all) != 3 {
		t.Fatalf("All() = %d, want 3", len(all))
	}
	if all[0].ID != "1" || all[2].ID != "3" {
		t.Errorf("order = %q,%q,%q, want 1,2,3", all[0].ID, all[1].ID, all[2].ID)
	}
}

func TestFilterByAgent(t *testing.T) {
	l := NewAuditLog()
	l.Record(entry("1", "a"))
	l.Record(entry("2", "b"))
	l.Record(entry("3", "a"))
	filtered := l.Filter("a")
	if len(filtered) != 2 {
		t.Fatalf("Filter(a) = %d, want 2", len(filtered))
	}
	for _, e := range filtered {
		if e.AgentID != "a" {
			t.Errorf("Filter returned agent %q, want a", e.AgentID)
		}
	}
	// Empty filter returns all.
	if got := l.Filter(""); len(got) != 3 {
		t.Errorf("Filter(\"\") = %d, want 3", len(got))
	}
	// Unknown agent returns none (not an error).
	if got := l.Filter("ghost"); len(got) != 0 {
		t.Errorf("Filter(ghost) = %d, want 0", len(got))
	}
}

func TestVerifyChainIntactForMemoryOnly(t *testing.T) {
	l := NewAuditLog()
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	if !l.VerifyChain() {
		t.Error("VerifyChain() = false for in-memory-only log, want true")
	}
}

func TestPersistAndVerifyChain(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	l := NewAuditLog().WithStore(store)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	l.Record(entry("", "c"))

	if !l.VerifyChain() {
		t.Fatal("VerifyChain() = false for intact persisted chain")
	}

	// Every entry must be persisted to the store.
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("store has %d entries, want 3", len(entries))
	}
	var loaded AuditEntry
	for _, e := range entries {
		if err := json.Unmarshal(e.Value, &loaded); err != nil {
			t.Fatalf("unmarshal %q: %v", e.Key, err)
		}
		if loaded.Hash == "" {
			t.Errorf("persisted entry %q has empty hash", e.Key)
		}
	}
}

func TestTamperBreaksChain(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	l := NewAuditLog().WithStore(store)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	l.Record(entry("", "c"))
	if !l.VerifyChain() {
		t.Fatal("chain should be intact before tampering")
	}

	// Tamper with the middle entry in memory; the chain must break.
	all := l.All()
	all[1].AgentID = "evil-agent"
	if l.VerifyChain() {
		t.Error("VerifyChain() = true after tampering with an entry, want false")
	}
}

// TestVerifyChainLegacyValidationOutcomeHash reproduces the 2026-08-31
// transition window: a binary that persisted ValidationOutcome but did not
// yet include it in the hash chain wrote entries whose stored hashes use the
// legacy formula. VerifyChain must accept those entries (the legacy formula
// covered a strict subset of fields), while still catching tampering.
func TestVerifyChainLegacyValidationOutcomeHash(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	l := NewAuditLog().WithStore(store)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	l.Record(entry("", "c"))
	if !l.VerifyChain() {
		t.Fatal("chain should be intact before rewriting entry 1's hash")
	}

	// Rewrite the middle entry's hash with the legacy formula, as the
	// in-transition binary would have persisted it.
	all := l.All()
	all[1].ValidationOutcome = &ValidationOutcome{Status: "WARN", ExitCode: 0,
		BlockedFiles: []string{"internal/app/platform.go"}, CorrelationID: "bp-1", Findings: 52}
	all[1].Hash = computeAuditHashLegacy(all[1], all[0].Hash)
	if !l.VerifyChain() {
		t.Fatal("VerifyChain() = false for legacy-formula entry with ValidationOutcome, want true")
	}
	// The chain after it still links via stored hashes.
	all[2].Hash = computeAuditHash(all[2], all[1].Hash)
	if !l.VerifyChain() {
		t.Fatal("VerifyChain() = false after relinking successor, want true")
	}

	// Tampering with the legacy entry's covered fields must still break.
	all[1].AgentID = "evil-agent"
	if l.VerifyChain() {
		t.Error("VerifyChain() = true after tampering with legacy entry, want false")
	}
}

func TestStorageFailureTolerated(t *testing.T) {
	// WithStore(nil) keeps the log fully in-memory and functional.
	l := NewAuditLog().WithStore(nil)
	l.Record(entry("", "a"))
	if len(l.All()) != 1 {
		t.Fatalf("All() = %d, want 1", len(l.All()))
	}
	if !l.VerifyChain() {
		t.Error("VerifyChain() = false, want true")
	}
}

func TestReplay(t *testing.T) {
	t.Run("no_store_is_noop", func(t *testing.T) {
		l := NewAuditLog()
		n, err := l.Replay()
		if err != nil {
			t.Fatalf("Replay() error without a store: %v", err)
		}
		if n != 0 {
			t.Fatalf("Replay() = %d entries without a store, want 0", n)
		}
	})

	t.Run("replays_persisted_entries_and_verifies", func(t *testing.T) {
		dir := t.TempDir()
		store := storage.NewLocal(dir)
		// Simulate a prior process: record (persists with hashes), then build a
		// fresh log over the same store.
		first := NewAuditLog().WithStore(store)
		first.Record(entry("", "a"))
		first.Record(entry("", "b"))

		fresh := NewAuditLog().WithStore(store)
		n, err := fresh.Replay()
		if err != nil {
			t.Fatalf("Replay(): %v", err)
		}
		if n != 2 {
			t.Fatalf("Replay() = %d entries, want 2", n)
		}
		if !fresh.VerifyChain() {
			t.Fatal("VerifyChain() = false after replaying an intact persisted chain")
		}
		if got := fresh.All(); len(got) != 2 || got[0].Hash == "" {
			t.Fatalf("replayed entries carry no hashes: %+v", got)
		}
	})

	t.Run("tampered_persisted_file_breaks_chain_after_replay", func(t *testing.T) {
		dir := t.TempDir()
		store := storage.NewLocal(dir)
		first := NewAuditLog().WithStore(store)
		first.Record(entry("", "a"))
		first.Record(entry("", "b"))
		if !first.VerifyChain() {
			t.Fatal("chain should be intact before tampering")
		}

		// Tamper with the persisted first entry (the file itself, not memory).
		entries, err := store.List(context.Background())
		if err != nil {
			t.Fatalf("List(): %v", err)
		}
		tampered := false
		for _, e := range entries {
			if e.Key == "audit-audit-1" {
				var ent AuditEntry
				if err := json.Unmarshal(e.Value, &ent); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				ent.AgentID = "evil-agent"
				raw, err := json.Marshal(ent)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				if err := store.Put(context.Background(), e.Key, raw); err != nil {
					t.Fatalf("Put tampered: %v", err)
				}
				tampered = true
			}
		}
		if !tampered {
			t.Fatal("tamper target audit-audit-1 not found in store")
		}

		fresh := NewAuditLog().WithStore(store)
		if _, err := fresh.Replay(); err != nil {
			t.Fatalf("Replay(): %v", err)
		}
		if fresh.VerifyChain() {
			t.Fatal("VerifyChain() = true after replaying a tampered persisted chain, want false")
		}
	})
}

// TestValidationOutcomeWireFormat pins the P0.4 wire convention for
// Blueprint's validation outcome: exported Go field names are the JSON keys
// (matching AuditEntry's untagged style), the field is optional on the entry,
// and it round-trips through the persisted form.
func TestValidationOutcomeWireFormat(t *testing.T) {
	// Unmarshal a Blueprint-authored outcome (untagged-style keys).
	raw := `{"Status":"BLOCK","ExitCode":1,"BlockedFiles":["foo.go"],"CorrelationID":"c1","Findings":2}`
	var vo ValidationOutcome
	if err := json.Unmarshal([]byte(raw), &vo); err != nil {
		t.Fatalf("unmarshal ValidationOutcome: %v", err)
	}
	if vo.Status != "BLOCK" || vo.ExitCode != 1 || vo.CorrelationID != "c1" || vo.Findings != 2 {
		t.Errorf("unmarshaled ValidationOutcome = %+v", vo)
	}
	if len(vo.BlockedFiles) != 1 || vo.BlockedFiles[0] != "foo.go" {
		t.Errorf("BlockedFiles = %v, want [foo.go]", vo.BlockedFiles)
	}

	// Round trip through an AuditEntry (marshal then unmarshal).
	e := AuditEntry{
		ID: "a1", AgentID: "blueprint", Action: "commit", Resource: "/repo",
		Result: "BLOCK",
		ValidationOutcome: &ValidationOutcome{
			Status: "ERROR", ExitCode: 3, BlockedFiles: []string{"x.go"},
			CorrelationID: "c9", Findings: 3,
		},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal AuditEntry: %v", err)
	}
	var back AuditEntry
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal AuditEntry: %v", err)
	}
	if back.ValidationOutcome == nil {
		t.Fatal("ValidationOutcome lost in AuditEntry round trip")
	}
	if back.ValidationOutcome.Status != "ERROR" || back.ValidationOutcome.CorrelationID != "c9" ||
		back.ValidationOutcome.ExitCode != 3 || back.ValidationOutcome.Findings != 3 {
		t.Errorf("round-tripped ValidationOutcome = %+v", back.ValidationOutcome)
	}

	// Legacy entries without the field parse to nil (backward compat).
	legacy := `{"ID":"a2","AgentID":"x","Action":"write","Resource":"source","Result":"allowed"}`
	var le AuditEntry
	if err := json.Unmarshal([]byte(legacy), &le); err != nil {
		t.Fatalf("unmarshal legacy entry: %v", err)
	}
	if le.ValidationOutcome != nil {
		t.Fatal("legacy entry should have nil ValidationOutcome")
	}

	// omitempty: a nil outcome does not appear in marshaled output.
	nilData, err := json.Marshal(AuditEntry{ID: "a3", AgentID: "x"})
	if err != nil {
		t.Fatalf("marshal nil-outcome entry: %v", err)
	}
	if strings.Contains(string(nilData), "ValidationOutcome") {
		t.Errorf("nil ValidationOutcome should be omitted (omitempty), got %s", nilData)
	}
}

// TestAppendExternalPersistsValidationOutcome: an external append carrying a
// validation outcome survives persist + replay with the field intact.
func TestAppendExternalPersistsValidationOutcome(t *testing.T) {
	store := storage.NewLocal(t.TempDir())
	l := NewAuditLog().WithStore(store)
	e := externalEntry()
	e.ValidationOutcome = &ValidationOutcome{
		Status: "BLOCK", ExitCode: 1, BlockedFiles: []string{"foo.go"},
		CorrelationID: "c1", Findings: 2,
	}
	if err := l.AppendExternal(e); err != nil {
		t.Fatalf("AppendExternal: %v", err)
	}
	if !l.VerifyChain() {
		t.Fatal("VerifyChain() = false after external append with ValidationOutcome")
	}

	fresh := NewAuditLog().WithStore(store)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	all := fresh.All()
	if len(all) != 1 {
		t.Fatalf("All() = %d entries, want 1", len(all))
	}
	vo := all[0].ValidationOutcome
	if vo == nil {
		t.Fatal("ValidationOutcome lost across persist + replay")
	}
	if vo.Status != "BLOCK" || len(vo.BlockedFiles) != 1 || vo.BlockedFiles[0] != "foo.go" || vo.CorrelationID != "c1" {
		t.Errorf("replayed ValidationOutcome = %+v", vo)
	}
}

func TestAuditLogPersistsToStore(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	l := NewAuditLog().WithStore(store)
	entry := AuditEntry{AgentID: "a1", Action: "write", Resource: "source", Risk: domain.Risk{Level: domain.RiskMedium}, Result: "allowed"}
	l.Record(entry)
	if len(l.All()) != 1 {
		t.Fatalf("All() = %d, want 1", len(l.All()))
	}

	// A new AuditLog over the same store must see the entry persisted by the
	// first instance.
	entries, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List() = %d entries, want 1", len(entries))
	}
	var loaded AuditEntry
	if err := json.Unmarshal(entries[0].Value, &loaded); err != nil {
		t.Fatalf("unmarshal persisted entry: %v", err)
	}
	if loaded.ID == "" {
		t.Error("persisted entry has empty ID")
	}
	if loaded.AgentID != "a1" {
		t.Errorf("persisted AgentID = %q, want a1", loaded.AgentID)
	}
	if loaded.Hash == "" {
		t.Error("persisted entry has empty hash")
	}
}

func TestAuditLogHashChain(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	l := NewAuditLog().WithStore(store)
	l.Record(AuditEntry{AgentID: "a", Action: "write", Resource: "f1", Result: "allowed"})
	l.Record(AuditEntry{AgentID: "b", Action: "drop", Resource: "t", Result: "denied"})
	l.Record(AuditEntry{AgentID: "c", Action: "read", Resource: "s", Result: "allowed"})

	if !l.VerifyChain() {
		t.Fatal("VerifyChain() = false, want true for intact chain")
	}

	// Tamper with an entry in memory and verify the chain breaks.
	all := l.All()
	all[1].AgentID = "evil-agent"
	if l.VerifyChain() {
		t.Error("VerifyChain() = true after tampering with an entry, want false")
	}
}

func TestAuditLogInMemoryBackwardCompat(t *testing.T) {
	l := NewAuditLog() // no store
	l.Record(AuditEntry{ID: "x", AgentID: "a", Result: "allowed"})
	l.Record(AuditEntry{ID: "y", AgentID: "b", Result: "denied"})
	if len(l.All()) != 2 {
		t.Fatalf("All() = %d, want 2", len(l.All()))
	}
	if got := l.Filter("a"); len(got) != 1 {
		t.Errorf("Filter(a) = %d, want 1", len(got))
	}
	if !l.VerifyChain() {
		t.Error("VerifyChain() = false for in-memory-only log, want true")
	}
}

// TestReplayNumericOrder verifies that replayed entries are restored in
// numeric audit sequence (write order), not store key order. The store lists
// keys lexically ("audit-audit-1", "audit-audit-10", ...), which would
// scramble the tamper chain for any log with 10+ entries; Replay must sort by
// the numeric sequence so VerifyChain passes after a fresh process replays.
func TestReplayNumericOrder(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	first := NewAuditLog().WithStore(store)
	for i := 1; i <= 15; i++ {
		first.Record(entry("", fmt.Sprintf("agent-%d", i)))
	}
	if !first.VerifyChain() {
		t.Fatal("chain must verify in memory before replay")
	}

	fresh := NewAuditLog().WithStore(store)
	n, err := fresh.Replay()
	if err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	if n != 15 {
		t.Fatalf("Replay() = %d entries, want 15", n)
	}
	if !fresh.VerifyChain() {
		t.Fatal("chain must verify after replay in numeric order")
	}
}

// TestVerifyChainReport classifies chain breaks: -1/nil firstBroken for an
// intact chain, the tampered entry's index with a positive verified count for
// genuine tampering (the rest of the chain still verifies via stored hashes),
// and firstBroken=0 with verified=0 when nothing verifies — the signature of
// entries written by an older kern version with a different hash format.
func TestVerifyChainReport(t *testing.T) {
	t.Run("intact_chain", func(t *testing.T) {
		l := NewAuditLog()
		l.Record(entry("", "a"))
		l.Record(entry("", "b"))
		brk, verified := l.VerifyChainReport()
		if brk != -1 || verified != 2 {
			t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 2)", brk, verified)
		}
		if !l.VerifyChain() {
			t.Fatal("VerifyChain() = false for intact chain, want true")
		}
	})

	t.Run("tampered_middle_entry_reports_its_index", func(t *testing.T) {
		dir := t.TempDir()
		store := storage.NewLocal(dir)
		l := NewAuditLog().WithStore(store)
		l.Record(entry("", "a"))
		l.Record(entry("", "b"))
		l.Record(entry("", "c"))
		// Tamper with the second entry (index 1): rewrite its stored value
		// with different content but keep its stored hash.
		orig := l.All()[1]
		orig.AgentID = "tampered"
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal tampered entry: %v", err)
		}
		if err := store.Put(context.Background(), "audit-"+orig.ID, data); err != nil {
			t.Fatalf("Put tampered entry: %v", err)
		}
		// A fresh process replays the tampered value from the store.
		fresh := NewAuditLog().WithStore(store)
		if _, err := fresh.Replay(); err != nil {
			t.Fatalf("Replay(): %v", err)
		}
		brk, verified := fresh.VerifyChainReport()
		if brk != 1 || verified != 2 {
			t.Fatalf("VerifyChainReport() = (%d, %d) after tampering entry 1, want (1, 2)", brk, verified)
		}
		if fresh.VerifyChain() {
			t.Fatal("VerifyChain() = true after tampering, want false")
		}
	})

	t.Run("all_unverifiable_is_legacy_signature", func(t *testing.T) {
		dir := t.TempDir()
		store := storage.NewLocal(dir)
		l := NewAuditLog().WithStore(store)
		l.Record(entry("", "a"))
		l.Record(entry("", "b"))
		// Rewrite both persisted entries with an alien hash, as an older kern
		// version with a different hash format would have written.
		for _, e := range l.All() {
			e.Hash = "0000000000000000000000000000000000000000000000000000000000000000"
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("marshal legacy entry: %v", err)
			}
			if err := store.Put(context.Background(), "audit-"+e.ID, data); err != nil {
				t.Fatalf("Put legacy entry: %v", err)
			}
		}
		fresh := NewAuditLog().WithStore(store)
		if _, err := fresh.Replay(); err != nil {
			t.Fatalf("Replay(): %v", err)
		}
		brk, verified := fresh.VerifyChainReport()
		if brk != 0 || verified != 0 {
			t.Fatalf("VerifyChainReport() = (%d, %d) for legacy entries, want (0, 0)", brk, verified)
		}
	})
}

// --- Cross-process writer fix: tail-accurate, locked writes ---

// TestStaleHeadChainsFromPersistedTail is the regression test for the
// concurrent-writer bug: process B holds an in-memory chain head from an
// older tail; when process A appends entries in between, B's next write must
// re-read the TRUE persisted tail and chain from it, not from its stale head.
func TestStaleHeadChainsFromPersistedTail(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	lock := filepath.Join(dir, ".lock")

	A := NewAuditLog().WithStore(store).WithLockPath(lock)
	A.Record(entry("", "a"))
	A.Record(entry("", "b"))

	B := NewAuditLog().WithStore(store).WithLockPath(lock)
	if _, err := B.Replay(); err != nil {
		t.Fatalf("B.Replay(): %v", err)
	}

	A.Record(entry("", "c"))
	B.Record(entry("", "d"))

	C := NewAuditLog().WithStore(store)
	if _, err := C.Replay(); err != nil {
		t.Fatalf("C.Replay(): %v", err)
	}
	brk, verified := C.VerifyChainReport()
	if brk != -1 || verified != 4 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 4)", brk, verified)
	}
}

// TestAppendExternalStaleHead covers the Blueprint path: a fresh process that
// skipped Replay() appends onto a chain that another process extended in the
// meantime — the append must chain from the persisted tail, not an empty or
// stale head.
func TestAppendExternalStaleHead(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	lock := filepath.Join(dir, ".lock")

	A := NewAuditLog().WithStore(store).WithLockPath(lock)
	A.Record(entry("", "a"))
	A.Record(entry("", "b"))

	// Fresh-process path: build a log over the same store+lock but do NOT
	// Replay() before the append.
	B := NewAuditLog().WithStore(store).WithLockPath(lock)
	A.Record(entry("", "c"))
	if err := B.AppendExternal(entry("", "d")); err != nil {
		t.Fatalf("AppendExternal: %v", err)
	}

	C := NewAuditLog().WithStore(store)
	if _, err := C.Replay(); err != nil {
		t.Fatalf("C.Replay(): %v", err)
	}
	brk, verified := C.VerifyChainReport()
	if brk != -1 || verified != 4 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 4)", brk, verified)
	}
}

// TestConcurrentWritersKeepChainIntact hammers the store with two logs (as
// two processes would) writing interleaved entries; the flock + tail re-read
// must keep every entry on one contiguous chain with no ID overwrites.
func TestConcurrentWritersKeepChainIntact(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	lock := filepath.Join(dir, ".lock")

	A := NewAuditLog().WithStore(store).WithLockPath(lock)
	B := NewAuditLog().WithStore(store).WithLockPath(lock)

	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(useB bool) {
			defer wg.Done()
			log := A
			if useB {
				log = B
			}
			for i := 0; i < 15; i++ {
				log.Record(entry("", "agent-x"))
			}
		}(g == 1)
	}
	wg.Wait()

	fresh := NewAuditLog().WithStore(store)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	brk, verified := fresh.VerifyChainReport()
	if brk != -1 || verified != 30 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 30)", brk, verified)
	}
	if got := len(fresh.All()); got != 30 {
		t.Fatalf("len(All()) = %d, want 30 (no ID overwrites)", got)
	}
}

// TestRepairChainRechainsFromFirstBroken: RepairChain recomputes chain-link
// hashes from the first broken entry onward, preserving entry content.
func TestRepairChainRechainsFromFirstBroken(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	lock := filepath.Join(dir, ".lock")

	l := NewAuditLog().WithStore(store).WithLockPath(lock)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	l.Record(entry("", "c"))

	// Corrupt the middle entry's stored hash in the persisted file.
	corrupted := l.All()[1]
	corrupted.Hash = strings.Repeat("0", 64)
	data, err := json.Marshal(corrupted)
	if err != nil {
		t.Fatalf("marshal corrupted entry: %v", err)
	}
	if err := store.Put(context.Background(), "audit-"+corrupted.ID, data); err != nil {
		t.Fatalf("Put corrupted entry: %v", err)
	}

	fresh := NewAuditLog().WithStore(store)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	brk, _ := fresh.VerifyChainReport()
	if brk != 1 {
		t.Fatalf("VerifyChainReport() firstBroken = %d, want 1", brk)
	}

	n, err := fresh.RepairChain()
	if err != nil {
		t.Fatalf("RepairChain(): %v", err)
	}
	if n < 1 {
		t.Fatalf("RepairChain() = %d, want >= 1", n)
	}

	// A fresh log now verifies the whole chain, and entry content is
	// preserved — only the chain-link Hash differs from the corrupted value.
	repaired := NewAuditLog().WithStore(store)
	if _, err := repaired.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	brk, verified := repaired.VerifyChainReport()
	if brk != -1 || verified != 3 {
		t.Fatalf("after repair VerifyChainReport() = (%d, %d), want (-1, 3)", brk, verified)
	}
	orig := l.All()
	got := repaired.All()
	if len(got) != len(orig) {
		t.Fatalf("entry count = %d, want %d", len(got), len(orig))
	}
	for i := range got {
		if got[i].ID != orig[i].ID ||
			!got[i].Timestamp.Equal(orig[i].Timestamp) ||
			got[i].AgentID != orig[i].AgentID ||
			got[i].Action != orig[i].Action ||
			got[i].Result != orig[i].Result {
			t.Fatalf("entry %d content changed by repair: %+v vs %+v", i, got[i], orig[i])
		}
		if got[i].Hash == "" {
			t.Fatalf("entry %d has empty hash after repair", i)
		}
	}
}

// TestLegacyNoLockPathStillWorks: a log without WithLockPath still re-reads
// the persisted tail before each write and chains correctly (backward compat,
// single-writer safe).
func TestLegacyNoLockPathStillWorks(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)

	l := NewAuditLog().WithStore(store) // no lock path
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	l.Record(entry("", "c"))

	fresh := NewAuditLog().WithStore(store)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	brk, verified := fresh.VerifyChainReport()
	if brk != -1 || verified != 3 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 3)", brk, verified)
	}
}

// TestLegacyToChainContinuation: entries written via a LocalStore-backed
// AuditLog (legacy per-key files) are continued by a NewLog-backed AuditLog
// over the same directory — a fresh process — which Records more entries via
// the chain.jsonl append path. The chain must verify across the boundary and
// the "audit-N" IDs must continue without restart.
func TestLegacyToChainContinuation(t *testing.T) {
	dir := t.TempDir()

	// Phase 1: an older writer persisted to per-key files (LocalStore).
	legacy := storage.NewLocal(dir)
	l1 := NewAuditLog().WithStore(legacy)
	l1.Record(entry("", "a"))
	l1.Record(entry("", "b"))
	l1.Record(entry("", "c"))

	// Phase 2: a fresh process over the append-only chain store, same dir.
	chained := storage.NewLog(dir)
	l2 := NewAuditLog().WithStore(chained)
	if n, err := l2.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	} else if n != 3 {
		t.Fatalf("Replay() = %d entries, want 3 (legacy files must be read)", n)
	}
	l2.Record(entry("", "d"))
	l2.Record(entry("", "e"))

	all := l2.All()
	if len(all) != 5 {
		t.Fatalf("All() = %d entries, want 5", len(all))
	}
	if got := all[3].ID; got != "audit-4" {
		t.Errorf("entry 4 ID = %q, want audit-4 (sequence must continue)", got)
	}
	if got := all[4].ID; got != "audit-5" {
		t.Errorf("entry 5 ID = %q, want audit-5 (sequence must continue)", got)
	}
	brk, verified := l2.VerifyChainReport()
	if brk != -1 || verified != 5 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 5) across legacy → chain.jsonl boundary", brk, verified)
	}

	// On disk: the 3 legacy files remain and chain.jsonl holds the 2 new
	// entries as JSON lines.
	if fi, err := os.Stat(filepath.Join(dir, "audit-audit-3.json")); err != nil || fi.IsDir() {
		t.Errorf("legacy file audit-audit-3.json missing after migration: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "chain.jsonl"))
	if err != nil {
		t.Fatalf("read chain.jsonl: %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Errorf("chain.jsonl has %d lines, want 2", got)
	}
	entries, err := chained.List(context.Background())
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("store has %d entries, want 5 (3 legacy + 2 chain)", len(entries))
	}
}

// TestFreshLogChainsFromChainTail: a fresh AuditLog over a NewLog store that
// skips Replay() still chains its first write from the TRUE persisted tail
// via LastEntry — the same stale-head-writer guarantee the full re-list gave,
// now through the O(1) fast path.
func TestFreshLogChainsFromChainTail(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLog(dir)

	l1 := NewAuditLog().WithStore(store)
	l1.Record(entry("", "a"))
	l1.Record(entry("", "b"))

	// Fresh process, no Replay: the first write must continue the persisted
	// sequence and chain from the persisted tail hash.
	l2 := NewAuditLog().WithStore(store)
	l2.Record(entry("", "c"))
	all := l2.All()
	if len(all) != 1 {
		t.Fatalf("All() = %d entries, want 1", len(all))
	}
	if got := all[0].ID; got != "audit-3" {
		t.Errorf("ID = %q, want audit-3 (continuation without Replay)", got)
	}

	// A third log replays everything and must verify the whole chain.
	l3 := NewAuditLog().WithStore(store)
	if _, err := l3.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	brk, verified := l3.VerifyChainReport()
	if brk != -1 || verified != 3 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 3) across fresh-writer boundary", brk, verified)
	}
}

func TestMerkleTreeParallelAuditLogging(t *testing.T) {
	log := NewAuditLog()

	// Simulate 10 parallel agents concurrently recording audit entries
	const numAgents = 10
	const entriesPerAgent = 20
	var wg sync.WaitGroup
	wg.Add(numAgents)

	for a := 0; a < numAgents; a++ {
		agentID := fmt.Sprintf("agent-%d", a)
		go func(ag string) {
			defer wg.Done()
			for i := 0; i < entriesPerAgent; i++ {
				e := AuditEntry{
					AgentID:  ag,
					Action:   "tool_call",
					Resource: fmt.Sprintf("resource-%d", i),
					Risk:     domain.Risk{Level: domain.RiskLow},
					Result:   "allowed",
					TaskID:   "task-parallel",
				}
				root := log.RecordParallel(e)
				if root == "" {
					t.Errorf("expected non-empty Merkle root")
				}
			}
		}(agentID)
	}
	wg.Wait()

	all := log.All()
	if len(all) != numAgents*entriesPerAgent {
		t.Fatalf("expected %d entries, got %d", numAgents*entriesPerAgent, len(all))
	}

	// Verify Merkle tree root integrity
	if !log.VerifyMerkle() {
		t.Fatal("VerifyMerkle() = false for parallel-recorded entries, want true")
	}

	root := log.MerkleRoot()
	if root == "" {
		t.Fatal("expected valid Merkle root")
	}

	// Tamper test: tamper with one entry and verify integrity check fails
	tampered := NewAuditLog()
	for _, e := range all {
		tampered.Record(e)
	}
	if !tampered.VerifyMerkle() {
		t.Fatal("expected untampered log to verify")
	}
	// Tamper with one entry
	tampered.entries[5].Action = "tampered_action"
	if tampered.VerifyMerkle() {
		t.Fatal("expected tampered log to fail VerifyMerkle()")
	}
}

func TestIncrementalMerkleTreeEquivalence(t *testing.T) {
	// Verify that incremental tree root matches computeMerkleRoot for every N up to 256
	tree := NewMerkleTree()
	var leaves []string
	for i := 1; i <= 256; i++ {
		leaf := fmt.Sprintf("leaf-%d-hash", i)
		leaves = append(leaves, leaf)
		tree.Append(leaf)

		want := computeMerkleRoot(leaves)
		got := tree.Root()
		if got != want {
			t.Fatalf("N=%d: tree.Root() = %s, want computeMerkleRoot = %s", i, got, want)
		}
	}
}

func BenchmarkIncrementalMerkleAppend(b *testing.B) {
	tree := NewMerkleTree()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leaf := fmt.Sprintf("leaf-%d", i)
		tree.Append(leaf)
	}
}

func BenchmarkRecordParallelContention(b *testing.B) {
	log := NewAuditLog()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e := AuditEntry{
				AgentID:  "bench-agent",
				Action:   "tool_call",
				Resource: "bench-res",
				Risk:     domain.Risk{Level: domain.RiskLow},
				Result:   "allowed",
			}
			log.RecordParallel(e)
		}
	})
}

// TestAuditRetentionCapAndCounters verifies the B8 retention fix: the
// in-memory log is capped at maxAuditEntries while lifetime counters keep
// the true totals (so the web console's governance metrics stay O(1) and
// correct even after trimming).
func TestAuditRetentionCapAndCounters(t *testing.T) {
	l := NewAuditLog()
	for i := 0; i < maxAuditEntries+2500; i++ {
		switch i % 3 {
		case 0:
			l.Record(AuditEntry{Action: "write", Result: "blocked"})
		case 1:
			l.Record(AuditEntry{Action: "write", Result: "approved"})
		default:
			l.Record(AuditEntry{Action: "write", Result: "allowed"})
		}
	}
	if got := l.Len(); got != maxAuditEntries {
		t.Errorf("Len() = %d; want the retention cap %d", got, maxAuditEntries)
	}
	if got := l.TotalRecords(); got != int64(maxAuditEntries+2500) {
		t.Errorf("TotalRecords() = %d; want %d (lifetime, not the capped view)", got, maxAuditEntries+2500)
	}
	// blocked: every 3rd of the total → floor((7500+2)/3)... exact split of 7500: i%3==0 → 2500.
	if got := l.BlocksCount(); got != 2500 {
		t.Errorf("BlocksCount() = %d; want 2500", got)
	}
	if got := l.OverridesCount(); got != 2500 {
		t.Errorf("OverridesCount() = %d; want 2500", got)
	}
	// The most recent entries survive; the oldest were trimmed.
	all := l.All()
	if len(all) == 0 || all[len(all)-1].ID == "" {
		t.Error("recent entries must survive the trim")
	}
}

// TestAuditHashNilOutcomeMatchesLegacyFormat: entries without a validation
// outcome must hash byte-identically to the pre-P0.4 format, so chains
// recorded by older versions still verify; with an outcome the hash must
// differ (the field is now covered by the tamper chain).
func TestAuditHashNilOutcomeMatchesLegacyFormat(t *testing.T) {
	e := entry("", "x")
	if got, want := computeAuditHash(e, "prev"), legacyAuditHash(e, "prev"); got != want {
		t.Errorf("nil-ValidationOutcome hash = %s, want legacy format %s", got, want)
	}
	e.ValidationOutcome = &ValidationOutcome{Status: "BLOCK", ExitCode: 1, BlockedFiles: []string{"a.go"}, CorrelationID: "c1", Findings: 2}
	if got, want := computeAuditHash(e, "prev"), legacyAuditHash(e, "prev"); got == want {
		t.Error("hash with ValidationOutcome equals the legacy hash, want different")
	}
}

// legacyAuditHash replicates the pre-P0.4 tamper-hash format (the old
// computeAuditHash body) so the byte-compat contract is pinned in the test.
func legacyAuditHash(e AuditEntry, prevHash string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%s|%v|%v|%v|%s|%s", prevHash, e.ID, e.AgentID, e.Action, e.Resource, e.Timestamp.UnixNano(), e.Risk, e.Approved, e.Result, e.TaskID)
	return hex.EncodeToString(h.Sum(nil))
}

// TestTamperBreaksChainForValidationOutcome: the tamper chain must cover
// ValidationOutcome — modifying it in a persisted entry must break
// VerifyChain (it did not before A10).
func TestTamperBreaksChainForValidationOutcome(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLocal(dir)
	l := NewAuditLog().WithStore(store)
	e := externalEntry()
	e.ValidationOutcome = &ValidationOutcome{Status: "BLOCK", ExitCode: 1, BlockedFiles: []string{"a.go"}, CorrelationID: "c1", Findings: 2}
	if err := l.AppendExternal(e); err != nil {
		t.Fatalf("AppendExternal: %v", err)
	}
	if !l.VerifyChain() {
		t.Fatal("chain with a ValidationOutcome should verify intact")
	}
	all := l.All()
	all[0].ValidationOutcome.Status = "PASS"
	if l.VerifyChain() {
		t.Error("VerifyChain() = true after tampering with ValidationOutcome, want false")
	}
}

// TestRepairChainLogStoreNoDuplicates: repairing a LogStore-backed log (the
// production store since the mixed LocalStore→LogStore window) must rewrite
// the store atomically. A per-entry Put would APPEND a new chain line per
// repaired key without removing the prior line, leaving duplicate entries
// that break every later chain walk — the 2026-09-02 race-repair regression.
func TestRepairChainLogStoreNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewLog(dir)
	lock := filepath.Join(dir, ".lock")

	l := NewAuditLog().WithStore(store).WithLockPath(lock)
	l.Record(entry("", "a"))
	l.Record(entry("", "b"))
	l.Record(entry("", "c"))
	l.Record(entry("", "d"))

	// Corrupt the second entry's stored hash in the persisted store.
	corrupted := l.All()[1]
	corrupted.Hash = strings.Repeat("0", 64)
	data, err := json.Marshal(corrupted)
	if err != nil {
		t.Fatalf("marshal corrupted entry: %v", err)
	}
	if err := store.Put(context.Background(), "audit-"+corrupted.ID, data); err != nil {
		t.Fatalf("Put corrupted entry: %v", err)
	}

	fresh := NewAuditLog().WithStore(store).WithLockPath(lock)
	if _, err := fresh.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	n, err := fresh.RepairChain()
	if err != nil {
		t.Fatalf("RepairChain(): %v", err)
	}
	if n < 1 {
		t.Fatalf("RepairChain() = %d, want >= 1", n)
	}

	// The repaired store must hold exactly one entry per key and verify.
	check := NewAuditLog().WithStore(storage.NewLog(dir))
	if _, err := check.Replay(); err != nil {
		t.Fatalf("Replay(): %v", err)
	}
	if got := len(check.All()); got != 4 {
		t.Fatalf("after repair, log holds %d entries, want 4 (no duplicates)", got)
	}
	if brk, verified := check.VerifyChainReport(); brk != -1 || verified != 4 {
		t.Fatalf("VerifyChainReport() = (%d, %d), want (-1, 4)", brk, verified)
	}

	// Recording a new entry after repair must chain cleanly off the repaired
	// head (no orphaned appends from a stale in-memory chain).
	check.Record(entry("", "e"))
	check2 := NewAuditLog().WithStore(storage.NewLog(dir))
	if _, err := check2.Replay(); err != nil {
		t.Fatalf("Replay() after post-repair record: %v", err)
	}
	if brk, verified := check2.VerifyChainReport(); brk != -1 || verified != 5 {
		t.Fatalf("post-repair record: VerifyChainReport() = (%d, %d), want (-1, 5)", brk, verified)
	}
}
