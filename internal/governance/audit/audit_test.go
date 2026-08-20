package audit

import (
	"context"
	"encoding/json"
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