package governance

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
