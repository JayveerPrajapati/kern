package governance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/domain"
	"github.com/JayveerPrajapati/kern/internal/storage"
)

func TestNewAuditLogEmpty(t *testing.T) {
	l := NewAuditLog()
	if got := l.All(); len(got) != 0 {
		t.Errorf("All() = %d entries, want 0", len(got))
	}
	if got := l.Filter("agent-1"); len(got) != 0 {
		t.Errorf("Filter() = %d entries, want 0", len(got))
	}
}

func TestRecordAssignsIDAndTimestamp(t *testing.T) {
	l := NewAuditLog()
	entry := AuditEntry{AgentID: "a1", Action: "write", Resource: "source", Risk: domain.Risk{Level: domain.RiskMedium}, Result: "allowed"}
	l.Record(entry)
	all := l.All()
	if len(all) != 1 {
		t.Fatalf("All() = %d, want 1", len(all))
	}
	if all[0].ID == "" {
		t.Error("Record should assign an ID")
	}
	if all[0].Timestamp.IsZero() {
		t.Error("Record should assign a timestamp")
	}
	if all[0].AgentID != "a1" {
		t.Errorf("AgentID = %q, want a1", all[0].AgentID)
	}
}

func TestRecordPreservesProvidedFields(t *testing.T) {
	l := NewAuditLog()
	entry := AuditEntry{ID: "custom-id", AgentID: "a", Action: "drop", Resource: "database", Risk: domain.Risk{Level: domain.RiskCritical}, Approved: true, Result: "blocked"}
	l.Record(entry)
	got := l.All()[0]
	if got.ID != "custom-id" {
		t.Errorf("ID = %q, want custom-id", got.ID)
	}
	if got.Approved != true || got.Result != "blocked" {
		t.Errorf("Approved/Result = %v/%q", got.Approved, got.Result)
	}
}

func TestAllPreservesOrder(t *testing.T) {
	l := NewAuditLog()
	l.Record(AuditEntry{ID: "1", AgentID: "a", Result: "allowed"})
	l.Record(AuditEntry{ID: "2", AgentID: "b", Result: "denied"})
	l.Record(AuditEntry{ID: "3", AgentID: "a", Result: "pending"})
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
	l.Record(AuditEntry{AgentID: "a", Result: "allowed"})
	l.Record(AuditEntry{AgentID: "b", Result: "denied"})
	l.Record(AuditEntry{AgentID: "a", Result: "pending"})

	filtered := l.Filter("a")
	if len(filtered) != 2 {
		t.Fatalf("Filter(a) = %d, want 2", len(filtered))
	}
	for _, e := range filtered {
		if e.AgentID != "a" {
			t.Errorf("Filter returned agent %q, want a", e.AgentID)
		}
	}

	// empty filter returns all
	if got := l.Filter(""); len(got) != 3 {
		t.Errorf("Filter(\"\") = %d, want 3", len(got))
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
